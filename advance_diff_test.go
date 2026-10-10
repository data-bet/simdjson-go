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

// The functions that walk the tape an element at a time got a straight path for the
// common case, in front of the code they had before. The references below are that
// code, unchanged, and the tests walk both in lockstep over the corpus, over the corpus
// cut short and with its containers pointing out of bounds, from every tag on the tape.

func advanceReference(i *Iter) Type {
	i.off += i.addNext

	for {
		if i.off >= len(i.tape.Tape) {
			i.addNext = 0
			i.t = TagEnd
			return TypeNone
		}

		v := i.tape.Tape[i.off]
		i.t = Tag(v >> 56)
		i.off++
		i.cur = v & JSONVALUEMASK
		if i.t == TagNop {
			i.off += int(i.cur)
			continue
		}
		break
	}
	i.calcNext(false)
	if i.addNext < 0 {
		// We can't send error, so move to end.
		i.moveToEnd()
		return TypeNone
	}
	return TagToType[i.t]
}

// sameIter reports how two Iters differ, or "" if they do not: every field, and for the
// tape, which words it covers.
func sameIter(got, want *Iter) string {
	if got.off != want.off || got.addNext != want.addNext || got.cur != want.cur || got.t != want.t {
		return fmt.Sprintf("off %d addNext %d cur %d tag %v, want off %d addNext %d cur %d tag %v",
			got.off, got.addNext, got.cur, got.t, want.off, want.addNext, want.cur, want.t)
	}
	if len(got.tape.Tape) != len(want.tape.Tape) || cap(got.tape.Tape) != cap(want.tape.Tape) ||
		got.tape.Strings != want.tape.Strings || len(got.tape.Message) != len(want.tape.Message) {
		return fmt.Sprintf("tape of %d/%d words, want %d/%d",
			len(got.tape.Tape), cap(got.tape.Tape), len(want.tape.Tape), cap(want.tape.Tape))
	}
	return ""
}

// panics runs f and reports whether it panicked, so that a reference that panics on a
// corrupt tape can be held to the same.
func panics(f func()) (p bool) {
	defer func() {
		if recover() != nil {
			p = true
		}
	}()
	f()
	return false
}

// tagStarts returns the offsets of the tag words on a tape: every word but the value
// words that follow strings and numbers. Long tapes are thinned out, evenly.
func tagStarts(tape []uint64) []int {
	var starts []int
	for off := 0; off < len(tape); off++ {
		starts = append(starts, off)
		switch Tag(tape[off] >> JSONTAGOFFSET) {
		case TagString, TagInteger, TagUint, TagFloat:
			off++
		}
	}
	if stride := len(starts)/20000 + 1; stride > 1 {
		thin := starts[:0]
		for k := 0; k < len(starts); k += stride {
			thin = append(thin, starts[k])
		}
		starts = thin
	}
	return starts
}

// stepsPerStart bounds each walk: far enough to cross a few siblings and the end of
// their container, and to catch a walk that does not end.
const stepsPerStart = 40

type diffTape struct {
	name   string
	pj     *ParsedJson
	starts []int
}

// diffTapes returns the corpus tapes and the variants of them that reach the paths the
// corpus does not: NOPs left by deletions, a tape cut short so that containers run past
// its end, and containers whose end points backwards or past the tape.
func diffTapes(t *testing.T) []diffTape {
	t.Helper()
	var out []diffTape
	nops := 0
	for _, tt := range testCases {
		pj := loadParsed(t, tt.name)
		starts := tagStarts(pj.Tape)
		out = append(out, diffTape{tt.name, pj, starts})

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
		for _, w := range deleted.Tape {
			if Tag(w>>JSONTAGOFFSET) == TagNop {
				nops++
			}
		}
		out = append(out, diffTape{tt.name + " with deletions", deleted, starts})

		for _, cut := range []int{len(pj.Tape) / 2, len(pj.Tape)/3 + 1} {
			short := *pj
			short.Tape = pj.Tape[:cut]
			var in []int
			for _, s := range starts {
				if s < cut {
					in = append(in, s)
				}
			}
			out = append(out, diffTape{fmt.Sprintf("%s cut to %d", tt.name, cut), &short, in})
		}

		bad := loadParsed(t, tt.name)
		k := 0
		for _, s := range starts {
			switch Tag(bad.Tape[s] >> JSONTAGOFFSET) {
			case TagRoot, TagObjectStart, TagArrayStart:
				tag := bad.Tape[s] &^ JSONVALUEMASK
				switch k++; k % 3 {
				case 0:
					bad.Tape[s] = tag // ends before it starts
				case 1:
					bad.Tape[s] = tag | uint64(len(bad.Tape)+5) // ends past the tape
				}
			}
		}
		out = append(out, diffTape{tt.name + " with bad ends", bad, starts})
	}
	if nops == 0 {
		t.Fatal("the deletions left no NOP on any tape, so the NOP paths went untested")
	}

	// The edges the corpus does not reach.
	edges := map[string][]uint64{
		"empty":                 {},
		"nop of zero":           {word(TagNop, 0), word(TagNull, 0)},
		"nop past the end":      {word(TagNull, 0), word(TagNop, 10)},
		"nop to the end":        {word(TagNull, 0), word(TagNop, 1)},
		"nops in a row":         {word(TagNop, 1), word(TagNop, 2), word(TagNull, 0), word(TagNop, 1), word(TagBoolTrue, 0)},
		"value word at the end": {word(TagInteger, 0)},
		"container at the end":  {word(TagArrayStart, 1)},
		"key at the end":        {word(TagString, 0), 0},
		"key and no value":      {word(TagString, 0), 0, word(TagObjectEnd, 0)},
		"key out of the area":   {word(TagString, 1<<40), 3, word(TagNull, 0), word(TagObjectEnd, 0)},
		"nop in an object":      {word(TagNop, 1), word(TagString, 0), 0, word(TagNull, 0), word(TagObjectEnd, 0)},
	}
	for name, tape := range edges {
		var starts []int
		for s := range tape {
			starts = append(starts, s)
		}
		out = append(out, diffTape{name, &ParsedJson{Tape: tape}, starts})
	}
	return out
}

func TestAdvanceMatchesReference(t *testing.T) {
	if !SupportedCPU() {
		t.SkipNow()
	}
	for _, dt := range diffTapes(t) {
		for _, start := range dt.starts {
			got, want := dt.pj.Iter(), dt.pj.Iter()
			got.off, want.off = start, start
			for step := 0; step < stepsPerStart; step++ {
				var gt, wt Type
				gp := panics(func() { gt = got.Advance() })
				wp := panics(func() { wt = advanceReference(&want) })
				if gp != wp {
					t.Fatalf("%s from %d, step %d: panicked %v, want %v", dt.name, start, step, gp, wp)
				}
				if gp {
					break
				}
				if diff := sameIter(&got, &want); gt != wt || diff != "" {
					t.Fatalf("%s from %d, step %d: type %v, want %v; %s", dt.name, start, step, gt, wt, diff)
				}
				if gt == TypeNone && got.t == TagEnd {
					break
				}
			}
		}
	}
}
