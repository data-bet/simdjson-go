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

// parseStringSimdValidateOnly scans the string starting at buf[1] without writing it
// out and reports the decoded length in dstLength.
//
// maxStringSize bounds the scan: the assembly stops looking that many bytes past
// buf[1] and fails if the closing quote has not turned up by then. It must be
// non-zero, since a zero bound reads as "no room at all" and fails immediately.
func parseStringSimdValidateOnly(buf []byte, maxStringSize, dstLength *uint64, needCopy *bool) bool {

	src := unsafe.Pointer(&buf[1]) // Use buf[1] in order to skip opening quote
	src_length := uint64(0)

	success := _parse_string_validate_only(src, unsafe.Pointer(maxStringSize), unsafe.Pointer(&src_length), unsafe.Pointer(dstLength))

	*needCopy = *needCopy || src_length != *dstLength
	return success != 0
}

// parseStringSimd decodes the string starting at buf[1] and appends it to *stringbuf.
//
// The caller must have reserved spare capacity for the decoded string plus the 32 byte
// overshoot of the copy loop: the assembly writes straight into the existing backing
// array, so the slice must never have to be reallocated.
func parseStringSimd(buf []byte, stringbuf *[]byte) bool {

	sb := *stringbuf
	// Indexing the full-capacity slice panics rather than corrupting memory if the
	// caller under-reserved.
	dst := unsafe.Pointer(&sb[:cap(sb)][len(sb)])
	stringBufLoc := dst

	src := unsafe.Pointer(&buf[1]) // Use buf[1] in order to skip opening quote

	res := _parse_string(src, dst, unsafe.Pointer(&stringBufLoc))

	*stringbuf = sb[:len(sb)+int(uintptr(stringBufLoc)-uintptr(dst))]

	return res != 0
}
