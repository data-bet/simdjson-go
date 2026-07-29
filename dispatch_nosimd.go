//go:build !amd64 || noasm || appengine || !gc
// +build !amd64 noasm appengine !gc

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

// The whole of the platform-dependent surface, for platforms with no assembly here
// and for any build that has asked for none. dispatch_amd64.go is the same list.

// SupportedCPU will return whether the CPU is supported.
//
// Always true here: this build has no assembly to be missing. Parsing goes through
// stage1_find_marks_generic.go and parse_string_generic.go, which produce byte for
// byte what the assembly does - see the digest in the commit that added them - at a
// fraction of the speed.
func SupportedCPU() bool { return true }

func findStructuralBitsInSlice(buf []byte, prevIterEndsOddBackslash *uint64,
	prevIterInsideQuote, errorMask *uint64,
	prevIterEndsPseudoPred *uint64,
	indexes *[indexSize]uint32, index *int, carried *uint64, position *uint64,
	ndjson uint64) (processed uint64) {

	return findStructuralBitsInSliceGeneric(buf, prevIterEndsOddBackslash,
		prevIterInsideQuote, errorMask, prevIterEndsPseudoPred,
		indexes, index, carried, position, ndjson)
}

func parseStringCopy(buf []byte, sb []byte) (size uint64, ok bool) {
	return parseStringGeneric(buf, sb)
}

func parseStringScan(buf []byte, maxStringSize, srcLength, dstLength *uint64) bool {
	return parseStringValidateOnlyGeneric(buf, maxStringSize, srcLength, dstLength)
}
