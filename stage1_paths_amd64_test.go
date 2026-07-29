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
	"testing"
)

// stage1Indexes runs stage 1 over msg and returns the structural positions it found,
// as absolute offsets so that the two implementations can be compared directly.
func stage1Indexes(t *testing.T, msg []byte) []uint32 {
	t.Helper()
	pj := &internalParsedJson{}
	pj.copyStrings = false
	pj.Message = msg
	pj.initialize(len(msg))
	pj.async = false
	if !pj.findStructuralIndices() {
		t.Fatalf("stage 1 rejected a %d byte document from the corpus", len(msg))
	}
	var out []uint32
	pos := ^uint64(0)
	for _, ic := range pj.indexList {
		for _, d := range ic.indexes[ic.index:ic.length] {
			pos += uint64(d)
			out = append(out, uint32(pos))
		}
	}
	return out
}

// Stage 1 carries two implementations of every step, one AVX2 and one AVX-512, and
// picks between them at runtime. The subroutine tests alongside this one do cover
// both halves - break either and four of them fail - but each checks one step
// against hand written input. What none of them reach is the two assembled pipelines
// running the same megabyte of JSON: the state carried from one 64 byte block to the
// next, the quote left dangling at the end of an index buffer, a document whose
// length is not a multiple of anything. That is where two implementations of the
// same idea drift apart without either looking wrong on its own.
//
// It skips where AVX-512 is missing, so it is a check for a developer who has it.
// CI has no machine that does, which is also why the AVX-512 path is worth keeping
// an eye on: dropping it was considered and measured at up to 5% on the files where
// stage 1 is the larger stage, so it stays.
func TestStage1PathsAgree(t *testing.T) {
	if !SupportedCPU() {
		t.SkipNow()
	}
	if !haveAVX512 {
		t.Skip("no AVX-512 on this CPU, so there is only one path to run")
	}
	// haveAVX512 is what selects the pipeline, so this is what has to be moved.
	defer func() { haveAVX512 = true }()

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			msg := loadCompressed(t, tt.name)

			haveAVX512 = true
			with := stage1Indexes(t, msg)

			haveAVX512 = false
			without := stage1Indexes(t, msg)
			haveAVX512 = true

			if len(with) != len(without) {
				t.Fatalf("AVX-512 found %d structural characters, AVX2 found %d", len(with), len(without))
			}
			for i := range with {
				if with[i] != without[i] {
					t.Fatalf("index %d of %d: AVX-512 says offset %d, AVX2 says %d",
						i, len(with), with[i], without[i])
				}
			}
		})
	}
}
