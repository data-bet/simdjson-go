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
	"encoding/binary"
	"errors"
	"math"
	"strconv"
	"unsafe"
)

const (
	isPartOfNumberFlag = 1 << iota
	isFloatOnlyFlag
	isMinusFlag
	isEOVFlag
	isDigitFlag
	isMustHaveDigitNext
	isDotFlag
)

var isNumberRune = [256]uint8{
	'0':  isPartOfNumberFlag | isDigitFlag,
	'1':  isPartOfNumberFlag | isDigitFlag,
	'2':  isPartOfNumberFlag | isDigitFlag,
	'3':  isPartOfNumberFlag | isDigitFlag,
	'4':  isPartOfNumberFlag | isDigitFlag,
	'5':  isPartOfNumberFlag | isDigitFlag,
	'6':  isPartOfNumberFlag | isDigitFlag,
	'7':  isPartOfNumberFlag | isDigitFlag,
	'8':  isPartOfNumberFlag | isDigitFlag,
	'9':  isPartOfNumberFlag | isDigitFlag,
	'.':  isPartOfNumberFlag | isFloatOnlyFlag | isMustHaveDigitNext | isDotFlag,
	'+':  isPartOfNumberFlag,
	'-':  isPartOfNumberFlag | isMinusFlag | isMustHaveDigitNext,
	'e':  isPartOfNumberFlag | isFloatOnlyFlag,
	'E':  isPartOfNumberFlag | isFloatOnlyFlag,
	',':  isEOVFlag,
	'}':  isEOVFlag,
	']':  isEOVFlag,
	' ':  isEOVFlag,
	'\t': isEOVFlag,
	'\r': isEOVFlag,
	'\n': isEOVFlag,
	':':  isEOVFlag,
}

// float64pow10 holds the powers of ten that are exactly representable as a
// float64. 10^22 is the last one: 10^23 already needs rounding.
var float64pow10 = [...]float64{
	1e0, 1e1, 1e2, 1e3, 1e4, 1e5, 1e6, 1e7, 1e8, 1e9, 1e10, 1e11,
	1e12, 1e13, 1e14, 1e15, 1e16, 1e17, 1e18, 1e19, 1e20, 1e21, 1e22,
}

// maxExactFloat64Int is the largest integer a float64 holds exactly: above it the
// significand runs out and consecutive integers start sharing a representation.
const maxExactFloat64Int = 1<<53 - 1

// atof64exact converts mant * 10^exp to a float64 using floating point
// arithmetic alone, and reports whether it was able to do so with the correctly
// rounded result.
//
// That holds when both operands are exact: mant has to be representable, and the
// power of ten has to be one of those below 10^23, so that a single multiply or
// divide rounds once and lands on the nearest float64.
//
// Positive exponents past 22 are still reachable by splitting off a factor of
// 10^22, but only while the intermediate product itself stays exact, for which 1e15
// is a conservative bound.
func atof64exact(mant uint64, exp int, neg bool) (float64, bool) {
	if mant > maxExactFloat64Int {
		return 0, false
	}
	f := float64(mant)
	if neg {
		f = -f
	}
	switch {
	case exp == 0:
		return f, true
	case exp > 0 && exp <= 22:
		return f * float64pow10[exp], true
	case exp < 0 && exp >= -22:
		return f / float64pow10[-exp], true
	case exp > 22 && exp <= 15+22:
		f *= float64pow10[exp-22]
		if f > 1e15 || f < -1e15 {
			return 0, false
		}
		return f * float64pow10[22], true
	}
	return 0, false
}

// fastInt folds the digits of an already validated integer token into a uint64.
// The caller guarantees the token is short enough not to overflow. ok is false when
// the token holds anything besides a leading minus and digits, which is left to
// strconv.
func fastInt(buf []byte) (mant uint64, ok bool) {
	i := 0
	if buf[0] == '-' {
		i = 1
	}
	for ; i < len(buf); i++ {
		d := buf[i] - '0'
		if d > 9 {
			break
		}
		mant = mant*10 + uint64(d)
	}
	return mant, i == len(buf) || isNumberRune[buf[i]] == isEOVFlag
}

// eightDigitsASCII reports whether all eight bytes of w, read little-endian from
// the message, are ASCII digits.
func eightDigitsASCII(w uint64) bool {
	return (w&0xF0F0F0F0F0F0F0F0)|
		(((w+0x0606060606060606)&0xF0F0F0F0F0F0F0F0)>>4) == 0x3333333333333333
}

// foldEightDigits turns eight ASCII digits into the number they spell, pairing them
// up and then combining the pairs. Folding them one at a time instead costs a
// multiply per digit, and each depends on the one before, so a long mantissa spends
// most of its time waiting on that chain.
func foldEightDigits(w uint64) uint64 {
	w -= 0x3030303030303030
	w = (w*10 + (w >> 8)) & 0x00FF00FF00FF00FF
	w = (w*100 + (w >> 16)) & 0x0000FFFF0000FFFF
	w = (w*10000 + (w >> 32)) & 0x00000000FFFFFFFF
	return w
}

// fastFloat folds the digits of a float token into mant, so that the token's value is
// mant * 10^exp10. The caller passes the token alone, and only when it has at most 16 digits, so
// mant cannot overflow.
//
// ok is false for the shapes it leaves to strconv: a second decimal point, stray signs and a
// malformed exponent.
func fastFloat(tok []byte) (mant uint64, exp10 int, ok bool) {
	dotAt := -1
	nd := 0
	i := 0
	if tok[0] == '-' {
		i = 1
	}
	for ; i < len(tok); i++ {
		c := tok[i]
		if d := c - '0'; d <= 9 {
			mant = mant*10 + uint64(d)
			nd++
			continue
		}
		if c == '.' && dotAt < 0 {
			dotAt = nd
			continue
		}
		break
	}
	if dotAt >= 0 {
		exp10 = dotAt - nd
	}
	if i < len(tok) {
		e, ok := parseExponent(tok[i:])
		if !ok || nd == 0 {
			return 0, 0, false
		}
		exp10 += e
	}
	return mant, exp10, true
}

// fastFloatLong does the same for a token long enough to be worth folding eight digits at a
// time, and that can hold more digits than a uint64 does.
//
// It is a separate function rather than a flag on fastFloat because restructuring that loop to
// accommodate both cost the files whose numbers are short 2-5%, even with the wide fold switched
// off - the flat loop above is simply better code for them.
//
// Leading zeros, on either side of the decimal point, are skipped before folding: they only move
// the decimal point, and without them nd counts the significant digits, which are what has to
// fit. Past 19 of those ok is false, and the token goes to strconv.
func fastFloatLong(tok []byte) (mant uint64, exp10 int, ok bool) {
	start := 0
	if tok[0] == '-' {
		start = 1
	}
	i := start
	for i < len(tok) && tok[i] == '0' {
		i++
	}
	dotAt := -1
	if i < len(tok) && tok[i] == '.' {
		dotAt = i
		i++
		for i < len(tok) && tok[i] == '0' {
			i++
		}
	}
	nd := 0
	// Two passes round this loop at most: the digits before the decimal point, then the ones
	// after it.
	for {
		for len(tok)-i >= 8 {
			w := binary.LittleEndian.Uint64(tok[i:])
			if !eightDigitsASCII(w) {
				break
			}
			mant = mant*100000000 + foldEightDigits(w)
			nd += 8
			i += 8
			if nd > 19 {
				return 0, 0, false
			}
		}
		for i < len(tok) {
			d := tok[i] - '0'
			if d > 9 {
				break
			}
			mant = mant*10 + uint64(d)
			nd++
			i++
		}
		if i < len(tok) && tok[i] == '.' && dotAt < 0 {
			dotAt = i
			i++
			continue
		}
		break
	}
	if nd > 19 {
		return 0, 0, false
	}
	if dotAt >= 0 {
		exp10 = dotAt + 1 - i
	}
	if i < len(tok) {
		digits := i - start - bool2[int](dotAt >= 0)
		e, ok := parseExponent(tok[i:])
		if !ok || digits == 0 {
			return 0, 0, false
		}
		exp10 += e
	}
	return mant, exp10, true
}

// parseExponent reads an exponent marker and the exponent after it, which have to run to the end
// of s. The value stops growing once it passes 10000, exactly as in strconv, so that both read the
// same exponent from any run of digits and the arithmetic cannot overflow.
func parseExponent(s []byte) (int, bool) {
	if s[0]|0x20 != 'e' || len(s) < 2 {
		return 0, false
	}
	i := 1
	neg := s[1] == '-'
	if neg || s[1] == '+' {
		i = 2
	}
	if i == len(s) {
		return 0, false
	}
	e := 0
	for ; i < len(s); i++ {
		d := s[i] - '0'
		if d > 9 {
			return 0, false
		}
		if e < 10000 {
			e = e*10 + int(d)
		}
	}
	if neg {
		e = -e
	}
	return e, true
}

// parseNumber will parse the number starting in the buffer.
// Any non-number characters at the end will be ignored.
// Returns TagEnd if no valid value found be found.
func parseNumber(buf []byte) (id, val uint64) {
	pos := 0
	found := uint8(0)
	for i, v := range buf {
		t := isNumberRune[v]
		if t == 0 {
			//fmt.Println("aborting on", string(v), "in", string(buf[:i]))
			return 0, 0
		}
		if t == isEOVFlag {
			break
		}
		if t&isMustHaveDigitNext > 0 {
			// A period and minus must be followed by a digit
			if len(buf) < i+2 || isNumberRune[buf[i+1]]&isDigitFlag == 0 {
				return 0, 0
			}
		}
		found |= t
		pos = i + 1
	}
	if pos == 0 {
		return 0, 0
	}
	const maxIntLen = 20
	floatTag := uint64(TagFloat) << JSONTAGOFFSET

	// The digit count is derived rather than counted: every byte of the token is a
	// digit except a leading minus and a decimal point. Anything else in there - a
	// stray sign, an exponent - only makes this an over-estimate, which is safe,
	// since the fast paths re-check the shape and refuse what they cannot handle.
	// Counting in the loop above instead cost more than the direct paths saved on
	// tokens too long to use them.
	nDigits := pos
	if found&isMinusFlag != 0 {
		nDigits--
	}
	if found&isDotFlag != 0 {
		nDigits--
	}

	// Only try integers if we didn't find any float exclusive and it can fit in an integer.
	if found&isFloatOnlyFlag == 0 && pos <= maxIntLen {
		if found&isMinusFlag == 0 {
			if pos > 1 && buf[0] == '0' {
				// Integers cannot have a leading zero.
				return 0, 0
			}
		} else {
			if pos > 2 && buf[1] == '0' {
				// Integers cannot have a leading zero after minus.
				return 0, 0
			}
		}
		// 19 digits always fit a uint64, so the value can be folded directly.
		if nDigits <= 19 {
			if mant, ok := fastInt(buf); ok {
				if found&isMinusFlag == 0 {
					if mant <= math.MaxInt64 {
						return uint64(TagInteger) << JSONTAGOFFSET, mant
					}
					return uint64(TagUint) << JSONTAGOFFSET, mant
				} else if mant <= 1<<63 {
					// 1<<63 wraps to math.MinInt64, which is the value we want.
					return uint64(TagInteger) << JSONTAGOFFSET, uint64(-int64(mant))
				}
				// Out of int64 range with a minus: strconv sets the range flag below.
			}
		}
		i64, err := strconv.ParseInt(unsafeBytesToString(buf[:pos]), 10, 64)
		if err == nil {
			return uint64(TagInteger) << JSONTAGOFFSET, uint64(i64)
		}
		if errors.Is(err, strconv.ErrRange) {
			floatTag |= uint64(FloatOverflowedInteger)
		}

		if found&isMinusFlag == 0 {
			u64, err := strconv.ParseUint(unsafeBytesToString(buf[:pos]), 10, 64)
			if err == nil {
				return uint64(TagUint) << JSONTAGOFFSET, u64
			}
			if errors.Is(err, strconv.ErrRange) {
				floatTag |= uint64(FloatOverflowedInteger)
			}
		}
	} else if found&isFloatOnlyFlag == 0 {
		floatTag |= uint64(FloatOverflowedInteger)
	}

	if pos > 1 && buf[0] == '0' && isNumberRune[buf[1]]&isFloatOnlyFlag == 0 {
		// Float can only have have a leading 0 when followed by a period.
		return 0, 0
	}
	// Up to 19 significant digits the folded mantissa is exact, and atof64exact or
	// parseFloat64 round it correctly; strconv is left the tokens with more and the
	// shapes the folds refuse. An integer-shaped token with more than 19 digits has
	// more than 19 significant ones too, short of leading zeros after a minus, so it
	// goes to strconv without a scan.
	if found&isFloatOnlyFlag != 0 || nDigits <= 19 {
		fold := fastFloat
		if nDigits > 16 {
			// Long enough that the digit-at-a-time multiply chain would dominate, and
			// too long to be sure that the digits fit.
			fold = fastFloatLong
		}
		if mant, exp10, ok := fold(buf[:pos]); ok {
			neg := buf[0] == '-'
			f64, ok := atof64exact(mant, exp10, neg)
			if !ok {
				f64, ok = parseFloat64(mant, exp10, neg)
			}
			if !ok {
				// The value rounds to infinity, which strconv rejects with ErrRange.
				return 0, 0
			}
			return floatTag, math.Float64bits(f64)
		}
	}
	f64, err := strconv.ParseFloat(unsafeBytesToString(buf[:pos]), 64)
	if err == nil {
		return floatTag, math.Float64bits(f64)
	}
	return 0, 0
}

// unsafeBytesToString should only be used when we have control of b, and only while b
// is not written to.
//
// unsafe.String rather than a reflect.StringHeader assembled through an unsafe.Pointer:
// that header's Data field is a uintptr, which the collector does not recognise as a
// reference, so between assembling the header and using the string nothing keeps the
// bytes alive. unsafe.String takes a real pointer and has no such window.
func unsafeBytesToString(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return unsafe.String(&b[0], len(b))
}
