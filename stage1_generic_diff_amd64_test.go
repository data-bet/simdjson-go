//go:build !noasm && !appengine && gc
// +build !noasm,!appengine,gc

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
	"math/rand"
	"strings"
	"testing"
)

// The portable stage 1 in stage1_find_marks_generic.go is what every platform without
// an assembly implementation runs. Nothing on amd64 calls it, so without these two
// tests it would be exercised only on machines nobody develops on, and would drift
// away from the assembly unnoticed. Here it runs against the assembly on the machine
// the change is being made on.
//
// The pair below splits the job: the first pins the byte classification, which is the
// only part the vector units actually do and the only part that had to be transcribed
// rather than reasoned out, and the second runs whole documents through both
// pipelines and compares every structural index and every piece of carried state.

// TestGenericClassificationMatchesAsm checks classifyBlockGeneric against
// find_whitespace_and_structurals for all 256 byte values, in every position within a
// block. The nibble tables it is built from are copied out of the assembly's data
// section by hand, so this is the test that catches a transcription slip.
func TestGenericClassificationMatchesAsm(t *testing.T) {
	if !SupportedCPU() {
		t.SkipNow()
	}
	rng := rand.New(rand.NewSource(7))

	for c := 0; c < 256; c++ {
		var block [64]byte
		// A block of one repeated value pins the value itself; the loop below then
		// re-tests it at a random position among unrelated bytes, so an error that
		// only shows up in one lane of a vector is still caught.
		for i := range block {
			block[i] = byte(c)
		}
		checkClassification(t, &block)

		for trial := 0; trial < 8; trial++ {
			for i := range block {
				block[i] = byte(rng.Intn(256))
			}
			block[rng.Intn(64)] = byte(c)
			checkClassification(t, &block)
		}
	}
}

func checkClassification(t *testing.T, block *[64]byte) {
	t.Helper()

	var wantWS, wantStruct uint64
	find_whitespace_and_structurals(block[:], &wantWS, &wantStruct)

	var wantOddPrev, gotOddPrev uint64
	wantBS := find_odd_backslash_sequences(block[:], &wantOddPrev)

	gotBS, _, gotWS, gotStruct, _, _ := classifyBlockGeneric(block)
	gotOdd := findOddBackslashSequencesGeneric(gotBS, &gotOddPrev)

	if gotWS != wantWS {
		t.Fatalf("block %q: whitespace mask %#016x, want %#016x", block[:], gotWS, wantWS)
	}
	if gotStruct != wantStruct {
		t.Fatalf("block %q: structural mask %#016x, want %#016x", block[:], gotStruct, wantStruct)
	}
	if gotOdd != wantBS || gotOddPrev != wantOddPrev {
		t.Fatalf("block %q: odd backslash ends %#016x/prev %d, want %#016x/prev %d",
			block[:], gotOdd, gotOddPrev, wantBS, wantOddPrev)
	}
}

// stage1DiffState is everything _find_structural_bits_in_slice carries from one call
// to the next, so that both implementations can be driven from identical state and
// compared on all of it rather than only on the indexes they emit.
type stage1DiffState struct {
	oddBackslash uint64
	insideQuote  uint64
	errorMask    uint64
	pseudoPred   uint64
	carried      uint64
	position     uint64
	length       int
	indexes      [indexSize]uint32
}

func newStage1DiffState() *stage1DiffState {
	return &stage1DiffState{pseudoPred: 1, position: ^uint64(0)}
}

func (s *stage1DiffState) run(buf []byte, ndjson uint64, asm bool) uint64 {
	if asm {
		return find_structural_bits_in_slice(buf, &s.oddBackslash,
			&s.insideQuote, &s.errorMask, &s.pseudoPred,
			&s.indexes, &s.length, &s.carried, &s.position, ndjson)
	}
	return findStructuralBitsInSliceGeneric(buf, &s.oddBackslash,
		&s.insideQuote, &s.errorMask, &s.pseudoPred,
		&s.indexes, &s.length, &s.carried, &s.position, ndjson)
}

// stage1DiffRun walks msg the way findStructuralIndices does - whole 64 byte blocks
// first, then the tail in a padded buffer - and returns every increment produced
// along with the final state.
func stage1DiffRun(t *testing.T, msg []byte, ndjson uint64, asm bool) ([]uint32, stage1DiffState) {
	t.Helper()
	s := newStage1DiffState()
	var out []uint32
	buf := msg
	for len(buf) > 0 {
		s.length = 0
		processed := s.run(buf[:len(buf) & ^63], ndjson, asm)
		if uint64(len(buf))-processed <= 64 {
			var pad [128]byte
			n := uint64(len(buf)) - processed
			copy(pad[:], buf[processed:])
			processed += s.run(pad[:n], ndjson, asm)
		}
		out = append(out, s.indexes[:s.length]...)
		if processed == 0 {
			t.Fatalf("stage 1 made no progress with %d bytes left", len(buf))
		}
		buf = buf[processed:]
	}
	final := stage1DiffState{
		oddBackslash: s.oddBackslash,
		insideQuote:  s.insideQuote,
		errorMask:    s.errorMask,
		pseudoPred:   s.pseudoPred,
		carried:      s.carried,
		position:     s.position,
	}
	return out, final
}

func stage1DiffCompare(t *testing.T, name string, msg []byte, ndjson uint64) {
	t.Helper()
	gotIdx, gotState := stage1DiffRun(t, msg, ndjson, false)
	wantIdx, wantState := stage1DiffRun(t, msg, ndjson, true)

	if len(gotIdx) != len(wantIdx) {
		t.Fatalf("%s: portable stage 1 found %d structural characters, assembly found %d",
			name, len(gotIdx), len(wantIdx))
	}
	pos := ^uint64(0)
	for i := range gotIdx {
		pos += uint64(wantIdx[i])
		if gotIdx[i] != wantIdx[i] {
			t.Fatalf("%s: index %d of %d (offset %d): portable says +%d, assembly says +%d",
				name, i, len(gotIdx), pos, gotIdx[i], wantIdx[i])
		}
	}
	if gotState != wantState {
		t.Fatalf("%s: carried state differs\nportable %+v\nassembly %+v", name, gotState, wantState)
	}
}

func TestGenericStage1MatchesAsm(t *testing.T) {
	if !SupportedCPU() {
		t.SkipNow()
	}

	// Shapes that put the interesting state on a block boundary: a quote, an escape
	// run or a number straddling the 64 byte edge is what carries between calls.
	var cases []string
	for n := 0; n <= 80; n++ {
		pad := strings.Repeat(" ", n)
		cases = append(cases,
			pad+`{"a":1}`,
			pad+`["`+strings.Repeat("x", n)+`"]`,
			pad+`{"k":"`+strings.Repeat(`\\`, n)+`"}`,
			pad+`{"k":"`+strings.Repeat(`\"`, n)+`"}`,
			pad+`[`+strings.Repeat("1,", n)+`1]`,
			pad+`{"`+strings.Repeat("a", n)+`":true}`,
		)
	}
	cases = append(cases, ``, ` `, `{}`, `[]`, `"`, `""`, `\`, `{"a":"\\\\"}`)

	for i, c := range cases {
		if len(c) == 0 {
			continue
		}
		stage1DiffCompare(t, clip(c), []byte(c), 0)
		stage1DiffCompare(t, clip(c)+" ndjson", []byte(c), 1)
		_ = i
	}

	// Random bytes reach block boundaries and mask combinations no hand written case
	// does, including sequences that are not valid JSON at all - stage 1 has to give
	// the same answer on those too, since it is what decides they are rejected.
	rng := rand.New(rand.NewSource(11))
	alphabet := []byte(`{}[]",:\ ` + "\t\n\r" + `abc012\u"`)
	for trial := 0; trial < 400; trial++ {
		n := 1 + rng.Intn(400)
		b := make([]byte, n)
		for i := range b {
			if trial%3 == 0 {
				b[i] = byte(rng.Intn(256))
			} else {
				b[i] = alphabet[rng.Intn(len(alphabet))]
			}
		}
		stage1DiffCompare(t, "random", b, uint64(trial&1))
	}
}

func TestGenericStage1MatchesAsmOnCorpus(t *testing.T) {
	if !SupportedCPU() {
		t.SkipNow()
	}
	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			msg := loadCompressed(t, tt.name)
			stage1DiffCompare(t, tt.name, msg, 0)
			stage1DiffCompare(t, tt.name+" ndjson", msg, 1)
		})
	}
}
