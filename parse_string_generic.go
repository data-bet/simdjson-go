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

import "bytes"

// The string parser without vector instructions, for platforms that have no assembly
// implementation here. It carries no build tag, so it is compiled everywhere; on
// amd64 nothing calls it, but parse_string_generic_diff_amd64_test.go runs it against
// the assembly, which is the only way the two are known to agree.
//
// The scan for the next quote or backslash goes through bytes.IndexByte rather than a
// loop, which is where most of the vector path's advantage is recovered: it is
// assembly on every architecture the runtime has one for, and a reasonable word-at-a-
// time loop everywhere else.
//
// That is also why arm64 has no assembly of its own here, although it does for stage 1
// in stage1_classify_arm64.s. The runtime's arm64 IndexByte is already NEON, so the
// byte scanning - the part with any width to exploit - is vectorised; what is left is
// the escape decoding, which is branchy and byte-at-a-time whatever it is written in.
// A bespoke copy-and-find like the AVX2 one would still be worth something, since it
// would fuse the copy into the scan, but not enough to write blind: there is no arm64
// hardware here to measure it on, and an assembly routine nobody has timed is a
// liability. The classification step in stage 1 was worth doing anyway because it has
// no equivalent in the standard library at all.
//
// Two tables below decide what an escape means, and both match the LCDATA1 block of
// parse_string_amd64.s byte for byte, which is what keeps the two implementations
// accepting the same input.

// escapeMap gives the byte a two-character escape stands for, or zero if there is no
// such escape. \u is not in it and is handled separately.
var escapeMap = func() (t [256]byte) {
	t['"'] = '"'
	t['/'] = '/'
	t['\\'] = '\\'
	t['b'] = 0x08
	t['f'] = 0x0c
	t['n'] = 0x0a
	t['r'] = 0x0d
	t['t'] = 0x09
	return t
}()

// digitToVal gives the value of a hex digit, or 0xff - read as -1, since the assembly
// sign-extends it - for any byte that is not one.
var digitToVal = func() (t [256]byte) {
	for i := 0; i < 256; i++ {
		t[i] = 0xff
	}
	for i := byte('0'); i <= '9'; i++ {
		t[i] = i - '0'
	}
	for i := byte('a'); i <= 'f'; i++ {
		t[i] = i - 'a' + 10
	}
	for i := byte('A'); i <= 'F'; i++ {
		t[i] = i - 'A' + 10
	}
	return t
}()

// hexQuad reads four hex digits the way the assembly does: each is sign-extended to
// 32 bits before being shifted into place, so one invalid digit poisons the whole
// value with high bits and every range check downstream then rejects it.
func hexQuad(s []byte) uint32 {
	d0 := uint32(int32(int8(digitToVal[s[0]])))
	d1 := uint32(int32(int8(digitToVal[s[1]])))
	d2 := uint32(int32(int8(digitToVal[s[2]])))
	d3 := uint32(int32(int8(digitToVal[s[3]])))
	return d0<<12 | d1<<8 | d2<<4 | d3
}

// decodeEscapeGeneric handles the escape sequence starting at s[0], which the caller
// has established is a backslash. It writes the decoded bytes to dst, or only counts
// them when dst is nil, and returns how much of s it consumed and how much it wrote.
//
// room is the distance from the backslash to the next quote character in the source.
// The assembly derives the same number from the 32-byte window it has in hand and
// uses it to refuse an escape whose digits would run past the end of the string; six
// bytes for \uXXXX and twelve for a surrogate pair.
func decodeEscapeGeneric(s []byte, room int, dst []byte) (consumed, written int, ok bool) {
	if len(s) < 2 {
		return 0, 0, false
	}
	if s[1] != 'u' {
		v := escapeMap[s[1]]
		if v == 0 {
			return 0, 0, false
		}
		if dst != nil {
			if len(dst) < 1 {
				return 0, 0, false
			}
			dst[0] = v
		}
		return 2, 1, true
	}

	if room < 6 || len(s) < 6 {
		return 0, 0, false
	}
	cp := hexQuad(s[2:6])
	consumed = 6

	if cp&0xfffffc00 == 0xd800 {
		if room < 12 || len(s) < 12 || s[6] != '\\' || s[7] != 'u' {
			return 0, 0, false
		}
		lo := hexQuad(s[8:12])
		// Either quad having come out poisoned shows up here as a value over
		// 0xffff, which is what makes an invalid hex digit an error.
		if cp|lo > 0xffff {
			return 0, 0, false
		}
		// Written the way the assembly combines them, in wrapping 32-bit
		// arithmetic, so that a low half that is not a low surrogate lands on the
		// same value here as it does there.
		cp = ((cp << 10) - 56623104) | (lo - 56320)
		cp += 65536
		consumed = 12
	}

	switch {
	case cp <= 0x7f:
		written = 1
	case cp <= 0x7ff:
		written = 2
	case cp <= 0xffff:
		written = 3
	case cp <= 0x10ffff:
		written = 4
	default:
		return 0, 0, false
	}

	if dst == nil {
		return consumed, written, true
	}
	if len(dst) < written {
		return 0, 0, false
	}
	switch written {
	case 1:
		dst[0] = byte(cp)
	case 2:
		dst[0] = byte(cp>>6) + 0xc0
		dst[1] = byte(cp)&0x3f | 0x80
	case 3:
		dst[0] = byte(cp>>12) + 0xe0
		dst[1] = byte(cp>>6)&0x3f | 0x80
		dst[2] = byte(cp)&0x3f | 0x80
	case 4:
		dst[0] = byte(cp>>18) + 0xf0
		dst[1] = byte(cp>>12)&0x3f | 0x80
		dst[2] = byte(cp>>6)&0x3f | 0x80
		dst[3] = byte(cp)&0x3f | 0x80
	}
	return consumed, written, true
}

// parseStringGeneric is the portable form of parseStringSimd: it decodes the string
// starting at buf[1] into the spare capacity of sb and reports how many bytes it
// wrote, leaving the caller to extend sb.
//
// Unlike the assembly it will not write past the capacity it was given, and returns
// false rather than running off the end of buf when there is no closing quote.
func parseStringGeneric(buf []byte, sb []byte) (uint64, bool) {
	if len(buf) < 1 {
		return 0, false
	}
	dst := sb[len(sb):cap(sb)]
	src := buf[1:] // skip the opening quote

	di, si := 0, 0
	for {
		rest := src[si:]
		q := bytes.IndexByte(rest, '"')
		if q < 0 {
			return 0, false
		}
		b := bytes.IndexByte(rest[:q], '\\')
		if b < 0 {
			if di+q > len(dst) {
				return 0, false
			}
			copy(dst[di:], rest[:q])
			return uint64(di + q), true
		}

		if di+b > len(dst) {
			return 0, false
		}
		copy(dst[di:], rest[:b])
		di += b
		si += b

		n, w, ok := decodeEscapeGeneric(src[si:], q-b, dst[di:])
		if !ok {
			return 0, false
		}
		si += n
		di += w
	}
}

// parseStringValidateOnlyGeneric is the portable form of
// parseStringSimdValidateOnly: it scans the string starting at buf[1] without writing
// it out, reporting the length of the body in the source and the length of its
// decoded form. maxStringSize bounds the scan and must be non-zero.
func parseStringValidateOnlyGeneric(buf []byte, maxStringSize, srcLength, dstLength *uint64) bool {
	limit := *maxStringSize
	if limit == 0 || len(buf) < 1 {
		return false
	}
	src := buf[1:] // skip the opening quote

	dl, si := 0, 0
	for {
		rest := src[si:]
		q := bytes.IndexByte(rest, '"')
		if q < 0 {
			return false
		}
		b := bytes.IndexByte(rest[:q], '\\')
		if b < 0 {
			if uint64(si+q) >= limit {
				return false
			}
			*srcLength = uint64(si + q)
			*dstLength = uint64(dl + q)
			return true
		}

		dl += b
		si += b

		n, w, ok := decodeEscapeGeneric(src[si:], q-b, nil)
		if !ok {
			return false
		}
		si += n
		dl += w
	}
}
