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

// advanceIntoReference is AdvanceInto as it was before its fast path was split out:
// the loop, then calcNext. AdvanceInto must leave an Iter in exactly the state this
// does, on every tape made of valid tags.
func advanceIntoReference(i *Iter) Tag {
	i.off += i.addNext
	for {
		if i.off >= len(i.tape.Tape) {
			i.addNext = 0
			i.t = TagEnd
			return TagEnd
		}

		v := i.tape.Tape[i.off]
		i.t = Tag(v >> 56)
		i.cur = v & JSONVALUEMASK
		if i.t == TagNop {
			if i.cur <= 0 {
				i.moveToEnd()
				return TagEnd
			}
			i.off += int(i.cur)
			continue
		}
		i.off++
		break
	}
	i.calcNext(true)
	if i.addNext < 0 {
		i.moveToEnd()
		return TagEnd
	}
	return i.t
}

// diffAdvanceInto walks pj with both implementations in lockstep and fails at the first
// step where the returned tag or any field of the Iter differs.
func diffAdvanceInto(t *testing.T, name string, pj *ParsedJson) {
	t.Helper()
	got, want := pj.Iter(), pj.Iter()
	for step := 0; ; step++ {
		// Every step moves forward by at least one word, so a walk that takes more
		// steps than the tape has words is not ending.
		if step > len(pj.Tape)+1 {
			t.Fatalf("%s: still walking after %d steps on a tape of %d", name, step, len(pj.Tape))
		}
		gt, wt := got.AdvanceInto(), advanceIntoReference(&want)
		if gt != wt || got.off != want.off || got.addNext != want.addNext ||
			got.cur != want.cur || got.t != want.t {
			t.Fatalf("%s, step %d: got tag %v off %d addNext %d cur %d, want tag %v off %d addNext %d cur %d",
				name, step, gt, got.off, got.addNext, got.cur, wt, want.off, want.addNext, want.cur)
		}
		if gt == TagEnd {
			return
		}
	}
}

func word(t Tag, v uint64) uint64 { return uint64(t)<<JSONTAGOFFSET | v }

func TestAdvanceIntoMatchesReference(t *testing.T) {
	if !SupportedCPU() {
		t.SkipNow()
	}
	nops := 0
	for _, tt := range testCases {
		pj := loadParsed(t, tt.name)
		diffAdvanceInto(t, tt.name, pj)

		// Deleting elements leaves NOP entries for the walk to skip.
		deleted := loadParsed(t, tt.name)
		n := 0
		_ = deleted.ForEach(func(i Iter) error {
			if obj, err := i.Object(nil); err == nil {
				_ = obj.DeleteElems(func(key []byte, i Iter) bool { n++; return n%2 == 0 }, nil)
			}
			if arr, err := i.Array(nil); err == nil {
				arr.DeleteElems(func(i Iter) bool { n++; return n%3 == 0 })
			}
			return nil
		})
		diffAdvanceInto(t, tt.name+" with deletions", deleted)
		for _, w := range deleted.Tape {
			if Tag(w>>JSONTAGOFFSET) == TagNop {
				nops++
			}
		}
	}
	if nops == 0 {
		t.Fatal("the deletions left no NOP on any tape, so the NOP path went untested")
	}

	// The edges the corpus does not reach.
	tapes := map[string][]uint64{
		"empty":                 {},
		"nop of zero":           {word(TagNop, 0), word(TagNull, 0)},
		"nop past the end":      {word(TagNull, 0), word(TagNop, 10)},
		"nop to the end":        {word(TagNull, 0), word(TagNop, 1)},
		"nops in a row":         {word(TagNop, 1), word(TagNop, 2), word(TagNull, 0), word(TagNop, 1), word(TagBoolTrue, 0)},
		"value word at the end": {word(TagInteger, 0)},
	}
	for name, tape := range tapes {
		diffAdvanceInto(t, name, &ParsedJson{Tape: tape})
	}
}

// TestAdvanceIntoValueTags pins the bit test that replaced calcNext's switch: for every
// valid tag it must skip the value word exactly when calcNext would.
func TestAdvanceIntoValueTags(t *testing.T) {
	for tag := range TagToType {
		tag := Tag(tag)
		if TagToType[tag] == TypeNone && tag != TagObjectEnd && tag != TagArrayEnd {
			continue // not a tag the parser writes
		}
		pj := &ParsedJson{Tape: []uint64{word(tag, 5), 0, word(TagNull, 0)}}
		diffAdvanceInto(t, fmt.Sprintf("tag %q", byte(tag)), pj)
	}
}

// TestAdvanceIntoAnyByte walks tapes whose tag bytes are anything at all. A byte that is
// not a tag may be read as one with a value word - the bit test sees only its low six
// bits - but the walk must still end, inside the tape, without a panic.
func TestAdvanceIntoAnyByte(t *testing.T) {
	for b := 0; b < 256; b++ {
		tape := []uint64{word(Tag(b), 1), word(Tag(b), 0), word(Tag(255-b), 3)}
		i := (&ParsedJson{Tape: tape}).Iter()
		for steps := 0; i.AdvanceInto() != TagEnd; steps++ {
			if i.off > len(tape) || steps > len(tape) {
				t.Fatalf("byte %#x: off %d after %d steps on a tape of %d", b, i.off, steps, len(tape))
			}
		}
	}
}
