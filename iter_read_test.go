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

import "testing"

// Reading a parsed document back had no benchmark at all, which left the whole
// consuming side of the library unmeasured - and easy to confuse with
// re-serialization, which does have one and is several times slower.
//
// Three shapes, in rising order of what a caller asks of the tape:
//
//	walk    descend everything, convert nothing. The floor.
//	typed   the same, pulling every leaf out as its Go type. A decoder into structs.
//	objects the same again through Object, so key names are read as well.
var readCorpus = []string{"apache_builds", "github_events", "citm_catalog", "twitter",
	"update-center", "canada", "instruments"}

func walkTape(pj *ParsedJson) int {
	n := 0
	i := pj.Iter()
	for i.AdvanceInto() != TagEnd {
		n++
	}
	return n
}

func walkTypedTape(tb testing.TB, pj *ParsedJson, stack []Iter) int {
	tb.Helper()
	n := 0
	stack = append(stack[:0], pj.Iter())
	for len(stack) > 0 {
		it := &stack[len(stack)-1]
		var err error
		switch tag := it.AdvanceInto(); tag {
		case TagEnd:
			stack = stack[:len(stack)-1]
			continue
		case TagString:
			_, err = it.StringBytes()
		case TagInteger:
			_, err = it.Int()
		case TagUint:
			_, err = it.Uint()
		case TagFloat:
			_, err = it.Float()
		case TagBoolTrue, TagBoolFalse, TagNull:
		default:
			continue
		}
		if err != nil {
			tb.Fatal(err)
		}
		n++
	}
	return n
}

func walkObjects(tb testing.TB, pj *ParsedJson, stack []Iter) int {
	tb.Helper()
	n := 0
	var obj Object
	var elem Iter
	stack = append(stack[:0], pj.Iter())
	for len(stack) > 0 {
		it := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for {
			typ := it.Advance()
			if typ == TypeNone {
				break
			}
			switch typ {
			case TypeRoot:
				var tmp Iter
				rt, r, err := it.Root(&tmp)
				if err != nil {
					tb.Fatal(err)
				}
				if rt != TypeNone {
					stack = append(stack, *r)
				}
			case TypeObject:
				o, err := it.Object(&obj)
				if err != nil {
					tb.Fatal(err)
				}
				for {
					name, et, err := o.NextElementBytes(&elem)
					if err != nil {
						tb.Fatal(err)
					}
					if et == TypeNone {
						break
					}
					n += len(name)
					if et == TypeObject || et == TypeArray {
						stack = append(stack, elem)
					}
				}
			case TypeArray:
				a, err := it.Array(nil)
				if err != nil {
					tb.Fatal(err)
				}
				stack = append(stack, a.Iter())
			}
		}
	}
	return n
}

func loadParsed(tb testing.TB, name string) *ParsedJson {
	tb.Helper()
	pj, err := Parse(loadCompressed(tb, name), nil)
	if err != nil {
		tb.Fatal(err)
	}
	return pj
}

// countScalars counts the leaves on the tape directly, so that a walk which
// silently stops early cannot be mistaken for a fast one.
func countScalars(pj *ParsedJson) int {
	n := 0
	for off := 0; off < len(pj.Tape); off++ {
		switch Tag(pj.Tape[off] >> JSONTAGOFFSET) {
		case TagString, TagInteger, TagUint, TagFloat:
			off++ // skip the value word
			n++
		case TagNull, TagBoolTrue, TagBoolFalse:
			n++
		}
	}
	return n
}

func TestReadWalksEverything(t *testing.T) {
	if !SupportedCPU() {
		t.SkipNow()
	}
	for _, name := range readCorpus {
		t.Run(name, func(t *testing.T) {
			pj := loadParsed(t, name)
			want := countScalars(pj)
			if got := walkTypedTape(t, pj, nil); got != want {
				t.Errorf("the typed walk read %d leaves, the tape holds %d", got, want)
			}
			if got := walkTape(pj); got < want {
				t.Errorf("the plain walk saw %d tags, fewer than the %d leaves on the tape", got, want)
			}
			if got := walkObjects(t, pj, nil); got == 0 && name != "canada" {
				t.Error("the object walk read no key names at all")
			}
		})
	}
}

func BenchmarkIterRead(b *testing.B) {
	if !SupportedCPU() {
		b.SkipNow()
	}
	for _, name := range readCorpus {
		pj := loadParsed(b, name)
		size := int64(len(pj.Message))
		stack := make([]Iter, 0, 64)

		b.Run(name+"/walk", func(b *testing.B) {
			b.SetBytes(size)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				walkTape(pj)
			}
		})
		b.Run(name+"/typed", func(b *testing.B) {
			b.SetBytes(size)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				walkTypedTape(b, pj, stack)
			}
		})
		b.Run(name+"/objects", func(b *testing.B) {
			b.SetBytes(size)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				walkObjects(b, pj, stack)
			}
		})
	}
}
