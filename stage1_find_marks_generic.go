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

import "math/bits"

// Stage 1 without any vector instructions, for platforms that have no assembly
// implementation here. It is the same algorithm: every step below turns a 64-byte
// block into 64-bit masks and then does bit arithmetic on them, and only the step
// that produces the masks in the first place needs a vector unit. That one becomes a
// table lookup per byte.
//
// This file carries no build tag, so it is compiled everywhere. Nothing calls it yet -
// the platforms that will are wired up in a later commit - but
// stage1_generic_diff_amd64_test.go runs it against the assembly over the corpus, which
// is the only way the two are known to agree.

// The AVX2 and AVX-512 paths classify a byte by looking its two nibbles up in a pair
// of 16-entry tables and ANDing the results, then testing bits of the product: 0x07
// for a structural character and 0x18 for whitespace. These are those two tables,
// transcribed from the LCDATA1 block of find_whitespace_and_structurals_amd64.s -
// the low-nibble one from offset 0x00 and the high-nibble one from offset 0x80.
//
// They are transcribed rather than written from the JSON grammar because they do not
// quite express it. The high half of wsStructHighNibble is non-zero, which taken at
// face value would make 0xb1 structural - its nibbles hit the same entries '{' does -
// and only the shuffle's own rule that an index byte with bit 7 set yields zero stops
// that from happening. buildJSONCharClass has to reproduce that rule, so getting the
// tables and the rule from the same place is what keeps the two in step.
var (
	wsStructLowNibble  = [16]byte{0x10, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x08, 0x0c, 0x01, 0x02, 0x09, 0x00, 0x00}
	wsStructHighNibble = [16]byte{0x08, 0x00, 0x12, 0x04, 0x00, 0x01, 0x00, 0x01, 0x00, 0x00, 0x00, 0x03, 0x02, 0x01, 0x00, 0x00}
)

// Bits of an entry of jsonCharClass.
const (
	classStructural = 1 << iota
	classWhitespace
	classQuote
	classBackslash
	classControl // below 0x20, which is an error inside a string
	classNewline
)

var jsonCharClass = buildJSONCharClass()

func buildJSONCharClass() (t [256]uint8) {
	for c := 0; c < 256; c++ {
		var class uint8
		// VPSHUFB yields zero for an index byte with bit 7
		// set, and the index for the low-nibble lookup is the input byte itself.
		// So a byte over 0x7f classifies as nothing at all, whatever the nibble
		// tables say about it. This is what stops the spurious entries described
		// above from ever being reached, and the reason the eight high entries of
		// wsStructHighNibble are dead code in every implementation.
		var v byte
		if c < 0x80 {
			v = wsStructLowNibble[c&0xf] & wsStructHighNibble[c>>4]
		}
		if v&0x07 != 0 {
			class |= classStructural
		}
		if v&0x18 != 0 {
			class |= classWhitespace
		}
		switch c {
		case '"':
			class |= classQuote
		case '\\':
			class |= classBackslash
		case '\n':
			class |= classNewline
		}
		if c < 0x20 {
			class |= classControl
		}
		t[c] = class
	}
	return t
}

// classifyBlockGeneric turns 64 bytes into the five masks the steps below consume,
// plus the newline mask ndjson mode needs. This is the whole of what the vector units
// are for here; everything after it is 64-bit integer work that is the same on every
// platform.
func classifyBlockGeneric(b *[64]byte) (backslash, quote, whitespace, structural, control, newline uint64) {
	for i := 0; i < 64; i++ {
		c := uint64(jsonCharClass[b[i]])
		structural |= (c >> 0 & 1) << i
		whitespace |= (c >> 1 & 1) << i
		quote |= (c >> 2 & 1) << i
		backslash |= (c >> 3 & 1) << i
		control |= (c >> 4 & 1) << i
		newline |= (c >> 5 & 1) << i
	}
	return
}

// findOddBackslashSequencesGeneric reports which characters are escaped: a backslash
// escapes the byte after it only when it ends a run of an odd number of them.
//
// The runs are found by adding: a carry propagates from the start of a run to one
// past its end, so the end of every run started on an even bit position lands on an
// odd one and vice versa. prev carries a run that was still open at the end of the
// previous block, as the single bit position the parity of the whole run depends on.
func findOddBackslashSequencesGeneric(bsBits uint64, prev *uint64) uint64 {
	const evenBits = uint64(0x5555555555555555)
	const oddBits = ^evenBits

	startEdges := bsBits &^ (bsBits << 1)
	evenStartMask := evenBits ^ *prev
	evenStarts := startEdges & evenStartMask
	oddStarts := startEdges &^ evenStartMask

	evenCarries := bsBits + evenStarts
	oddCarries, carry := bits.Add64(bsBits, oddStarts, 0)
	oddCarries |= *prev
	*prev = carry

	evenCarryEnds := evenCarries &^ bsBits
	oddCarryEnds := oddCarries &^ bsBits
	return (evenCarryEnds & oddBits) | (oddCarryEnds & evenBits)
}

// prefixXOR sets bit i to the XOR of bits 0..i of x, which turns a mask of quote
// characters into a mask of the characters between them.
//
// The vector paths get this from one carry-less multiply by all-ones; without CLMUL
// it takes six shift-and-XOR steps, each doubling the distance a bit has travelled.
func prefixXOR(x uint64) uint64 {
	x ^= x << 1
	x ^= x << 2
	x ^= x << 4
	x ^= x << 8
	x ^= x << 16
	x ^= x << 32
	return x
}

// finalizeStructuralsGeneric combines the masks into the final one, in which a set
// bit is a character stage 2 has to look at.
//
// That is the structural characters outside strings, the quotes that open a string,
// and the first character of every run of anything else - a number or an atom is
// identified by where it starts, so the byte after whitespace or after a structural
// character counts even though it is neither.
func finalizeStructuralsGeneric(structurals, whitespace, quoteMask, quoteBits uint64, prevIterEndsPseudoPred *uint64) uint64 {
	structurals &^= quoteMask
	structurals |= quoteBits

	pseudoPred := structurals | whitespace
	followsPseudoPred := (pseudoPred << 1) | *prevIterEndsPseudoPred
	*prevIterEndsPseudoPred = pseudoPred >> 63

	structurals |= followsPseudoPred &^ whitespace &^ quoteMask

	// Drop the closing quote of every string: quoteBits holds both ends of the pair
	// and only the opening one, which is inside quoteMask, is wanted.
	return structurals &^ (quoteBits &^ quoteMask)
}

// flattenBitsIncrementalGeneric is the portable form of __flatten_bits_incremental;
// see flatten_bits_amd64.s for what the increments and the carry mean.
func flattenBitsIncrementalGeneric(base *[indexSize]uint32, baseIndex *int, mask uint64, carried, position *uint64) {
	if mask == 0 {
		*carried += 64
		return
	}

	lead := uint64(bits.LeadingZeros64(mask))
	*position += *carried + 64 - lead

	// The previous structural character sits at -(carried+1) relative to this block,
	// so seeding prev with it makes every increment a plain difference of positions.
	prev := -(int64(*carried) + 1)
	*carried = lead

	idx := *baseIndex
	for m := mask; m != 0; m &= m - 1 {
		p := int64(bits.TrailingZeros64(m))
		base[idx] = uint32(p - prev)
		prev = p
		idx++
	}
	*baseIndex = idx
}

// findStructuralBitsInSliceGeneric is the portable form of
// _find_structural_bits_in_slice, and keeps its contract exactly: it consumes whole
// 64-byte blocks, stops early once the index buffer is nearly full, and returns how
// many bytes of buf it got through.
//
// A trailing partial block is copied into a spare one padded with spaces rather than
// read short, which is what the assembly's masking tail does. Padding with a space
// rather than a zero byte matters: a zero byte inside a string is an error, and the
// padding must not be able to produce one.
func findStructuralBitsInSliceGeneric(buf []byte, prevIterEndsOddBackslash *uint64,
	prevIterInsideQuote, errorMask *uint64,
	prevIterEndsPseudoPred *uint64,
	indexes *[indexSize]uint32, index *int, carried *uint64, position *uint64,
	ndjson uint64) (processed uint64) {

	if len(buf) == 0 {
		return 0
	}

	length := uint64(len(buf))
	whole := length &^ 63
	offset := uint64(0)

	var pad [64]byte
	padded := false

	for {
		var block *[64]byte
		if offset < whole {
			// Panics rather than reading out of bounds if the arithmetic above is
			// ever wrong; offset and whole are both multiples of 64.
			block = (*[64]byte)(buf[offset : offset+64])
			offset += 64
		} else {
			tail := length & 63
			if tail == 0 || padded {
				return offset
			}
			padded = true
			for i := range pad {
				pad[i] = ' '
			}
			copy(pad[:], buf[offset:])
			offset += tail
			block = &pad
		}

		backslash, quote, whitespace, structural, control, newline := classifyBlockGeneric(block)

		quote &^= findOddBackslashSequencesGeneric(backslash, prevIterEndsOddBackslash)

		quoteMask := prefixXOR(quote) ^ *prevIterInsideQuote
		*errorMask |= control & quoteMask
		*prevIterInsideQuote = uint64(int64(quoteMask) >> 63)

		structurals := finalizeStructuralsGeneric(structural, whitespace, quoteMask, quote, prevIterEndsPseudoPred)
		if ndjson != 0 {
			structurals |= newline &^ quoteMask
		}

		flattenBitsIncrementalGeneric(indexes, index, structurals, carried, position)

		if *index >= int(indexSizeWithSafetyBuffer) {
			return offset
		}
	}
}
