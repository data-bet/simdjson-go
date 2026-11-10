// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE-go file.

// Modified 2026 by DATA.BET; the modifications are released under the same terms.

package simdjson

import (
	"math"
	"math/bits"
)

// Floating point binary<->decimal conversion by fast unrounded scaling: one 64x128 bit multiply
// by a power of ten keeps enough information to round correctly in either direction. See
// "Floating-Point Printing and Parsing Can Be Simple And Fast", https://research.swtch.com/fp.

const (
	float64MantBits = 52
	// float64MinExp is the binary exponent of the smallest normal float64 once its mantissa is
	// left-justified in 64 bits; below it shortFloat64 is formatting a subnormal.
	float64MinExp = -1085
)

// bool2 converts b to an integer: 1 for true, 0 for false.
func bool2[T ~int | ~uint64](b bool) T {
	if b {
		return 1
	}
	return 0
}

// An unrounded is a value times 4 with a sticky bit in the lowest place: the bit below the
// binary point decides rounding, the sticky bit records whether anything below that was non-zero.
type unrounded uint64

func (u unrounded) floor() uint64         { return uint64((u + 0) >> 2) }
func (u unrounded) round() uint64         { return uint64((u + 1 + (u>>2)&1) >> 2) }
func (u unrounded) ceil() uint64          { return uint64((u + 3) >> 2) }
func (u unrounded) nudge(δ int) unrounded { return u + unrounded(δ) }

// unmin returns the minimum unrounded that rounds to x.
func unmin(x uint64) unrounded {
	return unrounded(x<<2 - 2)
}

// log10Pow2 returns floor(x * log10(2)).
func log10Pow2(x int) int {
	// log10(2) ~ 0.30102999566 ~ 78913 / 2^18
	return (x * 78913) >> 18
}

// log2Pow10 returns floor(x * log2(10)).
func log2Pow10(x int) int {
	// log2(10) ~ 3.32192809489 ~ 108853 / 2^15
	return (x * 108853) >> 15
}

// skewed returns floor(log10(3/4 * 2^e)): at a power of two the gap below is half the gap above,
// so the rounding interval is lopsided.
func skewed(e int) int {
	return (e*631305 - 261663) >> 21
}

// A pmHiLo represents hi<<64 - lo.
type pmHiLo struct {
	hi uint64
	lo uint64
}

// A scaler holds the scaling constants prescale derives for an e, p pair.
type scaler struct {
	// Note: using pm pmHiLo here nudges uscale just over the inlining boundary. Don't.
	pmHi uint64
	pmLo uint64
	s    int
}

// prescale returns the scaling constants for e, p; lp must be log2Pow10(p). A pre.s outside
// [0, 64) means a buggy caller when formatting; when parsing, pre.s >= 64 is a value that
// underflows to zero, and the caller has to pick it off before calling uscale.
func prescale(pre *scaler, e, p, lp int) {
	pre.pmHi = pow10Tab[p-pow10Min].hi
	pre.pmLo = pow10Tab[p-pow10Min].lo
	pre.s = -(e + lp + 3)
}

// uscale returns unround(x * 2^e * 10^p) for the pre that prescale computed for e and p. x must
// be left-justified, with its high bit set.
func uscale(x uint64, c *scaler) unrounded {
	hi, mid := bits.Mul64(x, c.pmHi)
	s := c.s & 63 // make shifts cheaper
	if hi>>s<<s != hi {
		return unrounded(hi>>s | 1)
	}
	mid2, _ := bits.Mul64(x, c.pmLo)
	hi -= bool2[uint64](mid < mid2)
	return unrounded(hi>>s | bool2[uint64](mid-mid2 > 1))
}

// parseFloat64 rounds d * 10^p to the nearest float64, negated when neg is set. d can have at
// most 19 digits. ok is false when the result rounds to infinity, which strconv reports as
// ErrRange; a result that underflows is a zero of the right sign, as strconv returns it.
func parseFloat64(d uint64, p int, neg bool) (f float64, ok bool) {
	sign := bool2[uint64](neg) << 63
	switch {
	case d == 0, p < -345:
		return math.Float64frombits(sign), true
	case p > 310:
		return math.Float64frombits(sign | 0x7ff<<52), false
	}
	b := bits.Len64(d)
	lp := log2Pow10(p)
	e := min(1074, 53-b-lp)
	var pre scaler
	prescale(&pre, e-(64-b), p, lp)
	if pre.s >= 64 {
		return math.Float64frombits(sign), true
	}
	u := uscale(d<<(64-b), &pre)

	// Branch-free: when u rounds to 2^53 or more, halve it, keeping the sticky bit, and
	// lower e to match.
	s := bool2[int](u >= unmin(1<<53))
	u = u>>s | u&1
	e -= s

	return pack64(sign|u.round(), -e)
}

// pack64 returns m * 2^e for a 53-bit mantissa m that carries the sign in bit 63, and an
// exponent in range for it; ok is false when the result overflows to infinity.
func pack64(m uint64, e int) (float64, bool) {
	if m&(1<<52) == 0 {
		return math.Float64frombits(m), true
	}
	if e >= 0x7FF-1075 {
		return math.Float64frombits(m&(1<<63) | 0x7ff<<52), false
	}
	return math.Float64frombits(m&^(1<<52) | uint64(1075+e)<<52), true
}

// shortFloat64 returns the shortest decimal d * 10^p that reads back as m * 2^e, picking the one
// nearest to it when several are equally short. m must be left-justified, with its high bit set.
// d can end in zeros.
func shortFloat64(m uint64, e int) (d uint64, p int) {
	// In the first two branches z is a constant, which lets the compiler simplify a lot.
	var lower, upper uint64
	var odd int
	z := 63 - float64MantBits
	if m == 1<<63 && e > float64MinExp {
		p = -skewed(e + z)
		lower = m - 1<<(z-2) // m - 1/4 * 2**(e+z)
		upper = m + 1<<(z-1) // m + 1/2 * 2**(e+z)
		odd = int(m>>z) & 1
	} else if e >= float64MinExp {
		p = -log10Pow2(e + z)
		lower = m - 1<<(z-1) // m - 1/2 * 2**(e+z)
		upper = m + 1<<(z-1) // m + 1/2 * 2**(e+z)
		odd = int(m>>z) & 1
	} else {
		z = z + (float64MinExp - e)
		p = -log10Pow2(e + z)
		lower = m - 1<<(z-1) // m - 1/2 * 2**(e+z)
		upper = m + 1<<(z-1) // m + 1/2 * 2**(e+z)
		odd = int(m>>z) & 1
	}

	var pre scaler
	prescale(&pre, e, p, log2Pow10(p))
	dmin := uscale(lower, &pre).nudge(+odd).ceil()
	dmax := uscale(upper, &pre).nudge(-odd).floor()

	if d = dmax / 10; d*10 >= dmin {
		return d, -(p - 1)
	}
	if d = dmin; d < dmax {
		d = uscale(m, &pre).round()
	}
	return d, -p
}
