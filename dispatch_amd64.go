//go:build !noasm && !appengine && gc
// +build !noasm,!appengine,gc

/*
 * MinIO Cloud Storage, (C) 2020 MinIO, Inc.
 * Modifications copyright (c) 2026 DATA.BET
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

import "github.com/klauspost/cpuid/v2"

var wantFeatures = cpuid.CombineFeatures(cpuid.AVX2, cpuid.CLMUL)

var haveAVX512 = cpuid.CPU.Has(cpuid.AVX512F)

// SupportedCPU will return whether the CPU is supported.
//
// On amd64 this is still the AVX2 and CLMUL test it always was. The portable
// implementation added alongside the assembly is not reachable from this build: the
// two string routines are the hottest thing in stage 2, and giving them a runtime
// feature test to load and branch on measured at over 2% on string heavy documents -
// a real cost to every user, paid so that a pre-2013 CPU need not pass a build tag.
//
// A CPU that lands here can have the portable implementation by building with
// -tags=noasm, which is what the error from Parse says. Every platform that has no
// assembly at all gets it without asking.
func SupportedCPU() bool {
	return cpuid.CPU.HasAll(wantFeatures)
}

// findStructuralBitsInSlice picks between the two assembly pipelines. The choice is
// made once per buffer of indexes rather than once per 64 byte block, so it costs
// nothing measurable.
func findStructuralBitsInSlice(buf []byte, prevIterEndsOddBackslash *uint64,
	prevIterInsideQuote, errorMask *uint64,
	prevIterEndsPseudoPred *uint64,
	indexes *[indexSize]uint32, index *int, carried *uint64, position *uint64,
	ndjson uint64) (processed uint64) {

	if haveAVX512 {
		return find_structural_bits_in_slice_avx512(buf, prevIterEndsOddBackslash,
			prevIterInsideQuote, errorMask, prevIterEndsPseudoPred,
			indexes, index, carried, position, ndjson)
	}
	return find_structural_bits_in_slice(buf, prevIterEndsOddBackslash,
		prevIterInsideQuote, errorMask, prevIterEndsPseudoPred,
		indexes, index, carried, position, ndjson)
}
