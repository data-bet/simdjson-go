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

// atof64exact converts mant * 10^exp to a float64 using floating point
// arithmetic alone, and reports whether it was able to do so with the correctly
// rounded result.
//
// That holds when every step is exact: mant must fit the 52 bit significand, and
// the power of ten must be one of the exactly representable ones, so that a single
// multiply or divide rounds once and lands on the nearest float64. Larger positive
// exponents are still reachable by splitting off a factor of 10^22, as long as the
// intermediate product stays below 1e15 and therefore exact.
func atof64exact(mant uint64, exp int, neg bool) (float64, bool) {
	if mant>>52 != 0 {
		return 0, false
	}
	f := float64(mant)
	if neg {
		f = -f
	}
	switch {
	case exp == 0:
		return f, true
	case exp > 0 && exp <= 15+22:
		if exp > 22 {
			f *= float64pow10[exp-22]
			exp = 22
		}
		if f > 1e15 || f < -1e15 {
			return 0, false
		}
		return f * float64pow10[exp], true
	case exp < 0 && exp >= -22:
		return f / float64pow10[-exp], true
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

// fastFloat does the same for a token with a decimal point, also reporting how many
// digits there are and where the point falls among them, so that the value is
// mant * 10^(dotAt-nd).
//
// ok is false for the shapes it leaves to strconv: exponents, a second decimal
// point and stray signs. Exponents are worth skipping because real JSON barely uses
// them - under 0.3% of the numbers in any of the test corpora.
func fastFloat(buf []byte) (mant uint64, dotAt, nd int, ok bool) {
	dotAt = -1
	i := 0
	if buf[0] == '-' {
		i = 1
	}
	for ; i < len(buf); i++ {
		c := buf[i]
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
	if i < len(buf) && isNumberRune[buf[i]] != isEOVFlag {
		return 0, 0, 0, false
	}
	return mant, dotAt, nd, true
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
	// since fastNumber re-checks the shape and refuses what it cannot handle.
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
	// 15 digits keep the mantissa inside the 52 bit significand, which is what
	// atof64exact needs to stay exact. Longer tokens skip the second pass entirely
	// and go straight to strconv, so they pay nothing for it.
	if nDigits <= 15 {
		if mant, dotAt, nd, ok := fastFloat(buf); ok && dotAt >= 0 {
			if f64, ok := atof64exact(mant, dotAt-nd, buf[0] == '-'); ok {
				return floatTag, math.Float64bits(f64)
			}
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
