// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE-go file.

// Modified 2026 by DATA.BET; the modifications are released under the same terms.

package simdjson

import (
	"errors"
	"math"
	"math/bits"
)

// appendFloat writes f as ES6 number to string conversion does, which matches most other JSON
// generators (see golang.org/issue/6384 and golang.org/issue/14135): the shortest form that reads
// back as f, in plain decimal notation for 1e-6 <= |f| < 1e21 and in exponent notation without
// zero padding outside that range.
func appendFloat(dst []byte, f float64) ([]byte, error) {
	b := math.Float64bits(f)
	neg := b>>63 != 0
	if b<<1 == 0 {
		if neg {
			return append(dst, '-', '0'), nil
		}
		return append(dst, '0'), nil
	}
	exp := int(b>>float64MantBits) & 0x7ff
	if exp == 0x7ff {
		return nil, errors.New("INF or NaN number found")
	}
	mant := b & (1<<float64MantBits - 1)
	if exp == 0 {
		exp = 1
	} else {
		mant |= 1 << float64MantBits
	}
	s := 64 - bits.Len64(mant)
	d, p := trimZeros(shortFloat64(mant<<s, exp-1023-s-float64MantBits))
	if abs := math.Abs(f); abs >= 1e-6 && abs < 1e21 {
		return appendDecimal(dst, neg, d, p), nil
	}
	return appendExponent(dst, neg, d, p), nil
}

// trimZeros removes the trailing zeros of a non-zero d, which shortFloat64 leaves when the
// shortest form is shorter than the precision it scaled to. Each step multiplies by the inverse
// of 5^k modulo 2^64 and rotates out k bits: the result is d / 10^k when 10^k divides d and too
// large otherwise, so no step divides. Eight zeros at a time, then one at a time, measured faster
// than a fixed 16-8-4-2-1 sequence, for short decimals and full-length values alike.
func trimZeros(d uint64, p int) (uint64, int) {
	for {
		r := bits.RotateLeft64(d*0xc767074b22e90e21, -8)
		if r > math.MaxUint64/uint64(1e8) {
			break
		}
		d, p = r, p+8
	}
	for {
		r := bits.RotateLeft64(d*0xcccccccccccccccd, -1)
		if r > math.MaxUint64/uint64(1e1) {
			break
		}
		d, p = r, p+1
	}
	return d, p
}

// zeros pads the integers that appendDecimal writes; 1e21 bounds them to 20 zeros.
const zeros = "00000000000000000000"

// appendDecimal writes d * 10^p, for a d without trailing zeros and a value in [1e-6, 1e21), in
// plain decimal notation. The text is laid out in a stack buffer so that dst grows once.
func appendDecimal(dst []byte, neg bool, d uint64, p int) []byte {
	// Room for a sign and either 21 integer digits, or "0." with five zeros and 17 digits.
	var buf [32]byte
	nd := numDigits(d)
	dp := nd + p
	var end int
	switch {
	case p >= 0:
		end = 1 + dp
		formatBase10(buf[1:1+nd], d)
		copy(buf[1+nd:end], zeros)
	case dp > 0:
		// The integer digits move one place left to open the gap for the point.
		end = 2 + nd
		formatBase10(buf[2:end], d)
		copy(buf[1:1+dp], buf[2:2+dp])
		buf[1+dp] = '.'
	default:
		end = 3 - p
		buf[1], buf[2] = '0', '.'
		formatBase10(buf[3:end], d)
	}
	if neg {
		buf[0] = '-'
		return append(dst, buf[:end]...)
	}
	return append(dst, buf[1:end]...)
}

// appendExponent writes d * 10^p, for a d without trailing zeros, as d.ddde-x or d.ddde+x.
func appendExponent(dst []byte, neg bool, d uint64, p int) []byte {
	// Room for a sign, 17 digits and a point, and "e-324".
	var buf [32]byte
	nd := numDigits(d)
	// The digits go one place to the right, so that the first can move left over the point.
	formatBase10(buf[2:2+nd], d)
	buf[1] = buf[2]
	end := 2
	if nd > 1 {
		buf[2] = '.'
		end = 2 + nd
	}
	x := nd - 1 + p
	buf[end] = 'e'
	buf[end+1] = '+'
	if x < 0 {
		buf[end+1] = '-'
		x = -x
	}
	end += 2
	switch {
	case x >= 100:
		buf[end] = byte('0' + x/100)
		x %= 100
		end++
		fallthrough
	case x >= 10:
		buf[end], buf[end+1] = smalls[2*x], smalls[2*x+1]
		end += 2
	default:
		buf[end] = byte('0' + x)
		end++
	}
	if neg {
		buf[0] = '-'
		return append(dst, buf[:end]...)
	}
	return append(dst, buf[1:end]...)
}

// uint64pow10[x] is 10^x.
var uint64pow10 = [...]uint64{
	1, 1e1, 1e2, 1e3, 1e4, 1e5, 1e6, 1e7, 1e8, 1e9,
	1e10, 1e11, 1e12, 1e13, 1e14, 1e15, 1e16, 1e17, 1e18, 1e19,
}

// numDigits returns the number of decimal digits in d, which must be at least 1.
func numDigits(d uint64) int {
	nd := log10Pow2(bits.Len64(d))
	return nd + bool2[int](d >= uint64pow10[nd])
}

// smalls is the formatting of 00..99 concatenated.
const smalls = "00010203040506070809" +
	"10111213141516171819" +
	"20212223242526272829" +
	"30313233343536373839" +
	"40414243444546474849" +
	"50515253545556575859" +
	"60616263646566676869" +
	"70717273747576777879" +
	"80818283848586878889" +
	"90919293949596979899"

// formatBase10 writes u into a right-aligned, two digits per division, padding with leading zeros
// when a is longer than u needs.
func formatBase10(a []byte, u uint64) {
	nd := len(a)
	for nd >= 8 {
		x3210 := uint32(u % 1e8)
		u /= 1e8
		x32, x10 := x3210/1e4, x3210%1e4
		x1, x0 := (x10/100)*2, (x10%100)*2
		x3, x2 := (x32/100)*2, (x32%100)*2
		a[nd-1], a[nd-2] = smalls[x0+1], smalls[x0]
		a[nd-3], a[nd-4] = smalls[x1+1], smalls[x1]
		a[nd-5], a[nd-6] = smalls[x2+1], smalls[x2]
		a[nd-7], a[nd-8] = smalls[x3+1], smalls[x3]
		nd -= 8
	}

	x := uint32(u)
	if nd >= 4 {
		x10 := x % 1e4
		x /= 1e4
		x1, x0 := (x10/100)*2, (x10%100)*2
		a[nd-1], a[nd-2] = smalls[x0+1], smalls[x0]
		a[nd-3], a[nd-4] = smalls[x1+1], smalls[x1]
		nd -= 4
	}
	if nd >= 2 {
		x0 := (x % 1e2) * 2
		x /= 1e2
		a[nd-1], a[nd-2] = smalls[x0+1], smalls[x0]
		nd -= 2
	}
	if nd > 0 {
		a[0] = byte('0' + x)
	}
}
