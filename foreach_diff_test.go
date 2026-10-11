/*
 * Copyright (c) 2026 DATA.BET
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package simdjson

import (
	"fmt"
	"testing"
)

// Array.ForEach and Object.ForEach without a key filter read the next element before
// they call fn. The references below are the old loops. The tests compare the two from
// each tag on the tapes of the Advance tests. Some callbacks write to the tape, because
// the read before fn must give the same result after such a write.

func arrayForEachReference(a *Array, fn func(i Iter)) {
	i := a.Iter()
	for {
		t := i.Advance()
		if t == TypeNone {
			break
		}
		fn(i)
	}
}

func objectForEachReference(o *Object, fn func(key []byte, i Iter)) error {
	tmp := o.tape.Iter()
	tmp.off = o.off
	for {
		typ := tmp.Advance()
		if typ != TypeString || tmp.off+1 >= len(tmp.tape.Tape) {
			if typ == TypeNone {
				return nil
			}
			return fmt.Errorf("object: unexpected name tag %v", tmp.t)
		}
		name, err := tmp.tape.stringByteAt(tmp.cur, tmp.tape.Tape[tmp.off])
		if err != nil {
			return fmt.Errorf("getting object name: %w", err)
		}

		t := tmp.Advance()
		if t == TypeNone {
			return nil
		}
		fn(name, tmp)
	}
}

type dirty struct{ lo, hi int }

func (d *dirty) add(span dirty) {
	if span.lo >= span.hi {
		return
	}
	if d.hi == 0 || span.lo < d.lo {
		d.lo = span.lo
	}
	if span.hi > d.hi {
		d.hi = span.hi
	}
}

type forEachWrite struct {
	name     string
	everyNth int
	do       func(i Iter) (written dirty)
}

func setNull(i Iter) dirty {
	written := dirty{i.off - 1, i.off + i.addNext + 1}
	_ = i.SetNull()
	return written
}

var forEachWrites = []forEachWrite{
	{"nothing", 1, nil},
	{"to its element", 3, setNull},
	{"to the next element", 2, func(i Iter) dirty {
		if i.Advance() == TypeNone {
			return dirty{}
		}
		return setNull(i)
	}},
	{"to the element after the next", 2, func(i Iter) dirty {
		// In an object, this is the value of the next key: the third word that
		// ForEach reads before fn.
		if i.Advance() == TypeNone || i.Advance() == TypeNone {
			return dirty{}
		}
		return setNull(i)
	}},
	{"to the next element's value bits", 2, func(i Iter) dirty {
		// The tag does not change. Only a comparison of all of the word finds this
		// change.
		if i.Advance() == TypeNone {
			return dirty{}
		}
		i.tape.Tape[i.off-1] ^= 1
		return dirty{i.off - 1, i.off}
	}},
	{"to the next string's length", 2, func(i Iter) dirty {
		// In an object, this is the length of the next key: the second word that
		// ForEach reads before fn.
		if i.Advance() != TypeString || i.off >= len(i.tape.Tape) {
			return dirty{}
		}
		i.tape.Tape[i.off] ^= 1
		return dirty{i.off, i.off + 1}
	}},
	{"over the NOPs ahead", 1, func(i Iter) dirty {
		// The first NOP after the element changes to the word at the end of its NOP
		// run. Then tape[p] is equal to the word that Advance read, but Advance read
		// more than one word. Advance starts its skip at the word after a NOP.
		p := i.off + i.addNext
		q := p
		for q < len(i.tape.Tape) && Tag(i.tape.Tape[q]>>56) == TagNop {
			skip := int(i.tape.Tape[q] & JSONVALUEMASK)
			if skip <= 0 {
				return dirty{}
			}
			q += 1 + skip
		}
		if q == p || q >= len(i.tape.Tape) {
			return dirty{}
		}
		i.tape.Tape[p] = i.tape.Tape[q]
		return dirty{p, p + 1}
	}},
}

// ForEach cannot stop before the end. A panic in fn stops the walk after
// stepsPerStart calls.
type stopForEach struct{}

type forEachCall struct {
	name string
	iter Iter
}

func runForEach(w forEachWrite, d *dirty, walkTape []uint64, walk func(fn func(name []byte, i Iter)) error) (calls []forEachCall, err error, panicked bool) {
	defer func() {
		if r := recover(); r != nil {
			if _, stop := r.(stopForEach); !stop {
				panicked = true
			}
		}
	}()
	defer func() { d.hi = min(d.hi, len(walkTape)) }()
	err = walk(func(name []byte, i Iter) {
		calls = append(calls, forEachCall{string(name), i})
		if w.do != nil && (len(calls)-1)%w.everyNth == 0 {
			d.add(w.do(i))
		}
		if len(calls) == stepsPerStart {
			panic(stopForEach{})
		}
	})
	return calls, err, false
}

// A copy of the tape for each start made these tests take 90 seconds. For this
// reason, the tests copy each tape one time, and restore writes back only the changed
// words.
func forEachTapes(pj *ParsedJson) (got, want ParsedJson) {
	got, want = *pj, *pj
	got.Tape = append([]uint64(nil), pj.Tape...)
	want.Tape = append([]uint64(nil), pj.Tape...)
	return got, want
}

func restore(pj *ParsedJson, d dirty, tapes ...ParsedJson) {
	for _, tp := range tapes {
		copy(tp.Tape[d.lo:d.hi], pj.Tape[d.lo:d.hi])
	}
}

func compareForEach(t *testing.T, where string, got, want ParsedJson, d *dirty,
	gotRun, wantRun func(fn func(name []byte, i Iter)) error, w forEachWrite) {
	t.Helper()
	gc, ge, gp := runForEach(w, d, got.Tape, gotRun)
	wc, we, wp := runForEach(w, d, want.Tape, wantRun)
	if gp != wp {
		t.Fatalf("%s: panicked %v, want %v", where, gp, wp)
	}
	if len(gc) != len(wc) {
		t.Fatalf("%s: %d calls, want %d", where, len(gc), len(wc))
	}
	for k := range gc {
		if diff := sameIter(&gc[k].iter, &wc[k].iter); gc[k].name != wc[k].name || diff != "" {
			t.Fatalf("%s, call %d: %q, want %q; %s", where, k, gc[k].name, wc[k].name, diff)
		}
	}
	if !gp && !sameErr(ge, we) {
		t.Fatalf("%s: %v, want %v", where, ge, we)
	}
	for k := d.lo; k < d.hi; k++ {
		if got.Tape[k] != want.Tape[k] {
			t.Fatalf("%s: the tape at %d is %#x, want %#x", where, k, got.Tape[k], want.Tape[k])
		}
	}
}

func TestArrayForEachMatchesReference(t *testing.T) {
	if !SupportedCPU() {
		t.SkipNow()
	}
	for _, dt := range diffTapes(t) {
		got, want := forEachTapes(dt.pj)
		for _, w := range forEachWrites {
			for _, start := range dt.starts {
				var d dirty
				ga, wa := Array{tape: got, off: start}, Array{tape: want, off: start}
				compareForEach(t, fmt.Sprintf("%s from %d, writing %s", dt.name, start, w.name), got, want, &d,
					func(fn func(name []byte, i Iter)) error {
						ga.ForEach(func(i Iter) { fn(nil, i) })
						return nil
					},
					func(fn func(name []byte, i Iter)) error {
						arrayForEachReference(&wa, func(i Iter) { fn(nil, i) })
						return nil
					}, w)
				restore(dt.pj, d, got, want)
			}
		}
	}
}

func TestObjectForEachMatchesReference(t *testing.T) {
	if !SupportedCPU() {
		t.SkipNow()
	}
	members := 0
	for _, dt := range diffTapes(t) {
		got, want := forEachTapes(dt.pj)
		for _, w := range forEachWrites {
			for _, start := range dt.starts {
				var d dirty
				gotObj, wantObj := Object{tape: got, off: start}, Object{tape: want, off: start}
				compareForEach(t, fmt.Sprintf("%s from %d, writing %s", dt.name, start, w.name), got, want, &d,
					func(fn func(name []byte, i Iter)) error {
						return gotObj.ForEach(func(key []byte, i Iter) { members++; fn(key, i) }, nil)
					},
					func(fn func(name []byte, i Iter)) error {
						return objectForEachReference(&wantObj, fn)
					}, w)
				restore(dt.pj, d, got, want)
			}
		}
	}
	// Most starts are not at a key. Make sure that enough members went to fn.
	if members < 10000 {
		t.Fatalf("only %d members were handed to fn", members)
	}
}
