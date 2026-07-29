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

import (
	"unsafe"
)

//go:noescape
func _parse_string_validate_only(src, maxStringSize, str_length, dst_length unsafe.Pointer) (result uint64)

//go:noescape
func _parse_string(src, dst, pcurrent_string_buf_loc unsafe.Pointer) (res uint64)

// parseStringScan scans the string starting at buf[1] without writing
// it out. It reports how many source bytes the string body occupies in srcLength
// and how long its decoded form is in dstLength. The two are equal exactly when
// the string holds no escapes, since every escape sequence decodes to fewer bytes
// than it occupies.
//
// maxStringSize bounds the scan: the assembly stops looking that many bytes past
// buf[1] and returns failure if the closing quote has not been found by then. It
// must be non-zero, since the assembly treats a zero bound as "no room at all"
// and fails immediately - see parseString.
//
// Kept out of line on purpose. It is small enough to inline, but doing so leaves
// more live across the assembly call inside parseString, which measures as a
// regression on strings that need no copying - the case that does the least work
// after the scan and so has the least to hide the extra spills behind.
//
//go:noinline
func parseStringScan(buf []byte, maxStringSize, srcLength, dstLength *uint64) bool {

	src := unsafe.Pointer(&buf[1]) // Use buf[1] in order to skip opening quote

	success := _parse_string_validate_only(src, unsafe.Pointer(maxStringSize), unsafe.Pointer(srcLength), unsafe.Pointer(dstLength))

	return success != 0
}

// parseStringCopy decodes the string starting at buf[1] into the spare capacity of
// sb and reports how many bytes it wrote. The caller extends sb itself.
//
// The caller must have reserved room for the decoded string plus the 32 byte
// overshoot of the copy loop; the assembly writes straight into the existing
// backing array, so the slice must never have to be reallocated.
//
// Returning the length rather than writing back through a *[]byte keeps the slice
// header in the caller's registers. Reading it back through the pointer, right
// after the assembly had stored into the same stack slot, was half the cost of this
// function.
func parseStringCopy(buf []byte, sb []byte) (size uint64, ok bool) {

	// Indexing the full-capacity slice panics rather than corrupting memory if
	// the caller under-reserved.
	dst := unsafe.Pointer(&sb[:cap(sb)][len(sb)])
	stringBufLoc := dst

	src := unsafe.Pointer(&buf[1]) // Use buf[1] in order to skip opening quote

	res := _parse_string(src, dst, unsafe.Pointer(&stringBufLoc))

	return uint64(uintptr(stringBufLoc) - uintptr(dst)), res != 0
}
