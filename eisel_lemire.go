// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE-go file.
//
// This is a re-implementation of eiselLemire64 from the Go standard library's
// strconv package: the constants, the branch conditions and the order of the
// bail-outs are the standard library's, restructured for this parser's callers and
// commented afresh.
//
// Modified 2026 by DATA.BET; the modifications are released under the same terms.

package simdjson

import (
	"math"
	"math/bits"
)

// eiselLemire64 converts man * 10^exp10 to the nearest float64, following the
// algorithm Eisel and Lemire describe in "Number Parsing at a Gigabyte per Second":
// normalise the decimal mantissa, multiply it by a 128 bit approximation of the
// power of ten, and read the answer off the top of the product.
//
// It reports false rather than guessing. That happens when the product lands too
// near a rounding boundary for the approximation to settle, when the value sits
// exactly halfway between two float64s, and when the result would be subnormal or
// overflow. Those go to strconv, which is exact but has to walk the digits again.
//
// man must be non-zero - the caller handles zero, whose exponent arithmetic does not
// apply - and must be a mantissa of at most 19 decimal digits, so that it is exact.
//
// The powers of ten come from detailedPowersOfTen in ftoaryu.go, which the float
// formatter there already needs: each entry is that power's significand truncated to
// 128 bits and normalised so its top bit is set.
func eiselLemire64(man uint64, exp10 int, neg bool) (float64, bool) {
	if exp10 < detailedPowersOfTenMinExp10 || exp10 > detailedPowersOfTenMaxExp10 {
		return 0, false
	}

	// Line the mantissa up against the top of the word, and start the binary exponent
	// from exp10 * log2(10). 217706/65536 approximates log2(10) from below closely
	// enough over the table's range for the shift further down to correct it.
	clz := bits.LeadingZeros64(man)
	man <<= uint(clz)
	const float64Bias = 1023
	exp2 := uint64(217706*exp10>>16+64+float64Bias) - uint64(clz)

	pow := &detailedPowersOfTen[exp10-detailedPowersOfTenMinExp10]
	hi, lo := bits.Mul64(man, pow[1])

	// Only the top 55 bits of the product matter. If every bit below them is set, the
	// 64 bits the table dropped could have carried into them, so redo the
	// multiplication against the power's low word as well.
	if hi&0x1FF == 0x1FF && lo+man < man {
		yHi, yLo := bits.Mul64(man, pow[0])
		mergedHi, mergedLo := hi, lo+yHi
		if mergedLo < lo {
			mergedHi++
		}
		// Still undecided with 128 bits of the power in hand: hand it over.
		if mergedHi&0x1FF == 0x1FF && mergedLo+1 == 0 && yLo+man < man {
			return 0, false
		}
		hi, lo = mergedHi, mergedLo
	}

	// Keep 54 bits: the 53 of the significand plus one to round on. The product lies
	// in [2^126, 2^128), and its top bit says which half, so it also fixes the
	// exponent by one.
	msb := hi >> 63
	mantissa := hi >> (msb + 9)
	exp2 -= 1 ^ msb

	// Exactly halfway between two float64s with nothing below to break the tie. Round
	// half to even would go down here and the rounding below goes up, so this is the
	// one case where they disagree - and which is right depends on digits the table
	// truncated away.
	if lo == 0 && hi&0x1FF == 0 && mantissa&3 == 1 {
		return 0, false
	}

	// Round on the bit that was kept for it, then drop it.
	mantissa += mantissa & 1
	mantissa >>= 1
	if mantissa>>53 != 0 {
		mantissa >>= 1
		exp2++
	}

	// Zero or below is subnormal, 0x7FF or above is an infinity; neither is worked out
	// here. The subtraction wraps for exp2 == 0, which the unsigned compare catches.
	if exp2-1 >= 0x7FF-1 {
		return 0, false
	}
	out := exp2<<52 | mantissa&(1<<52-1)
	if neg {
		out |= 1 << 63
	}
	return math.Float64frombits(out), true
}
