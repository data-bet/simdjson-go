/*
 * Copyright (c) 2026 DATA.BET
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
	"bytes"
	"math"
	"math/rand/v2"
	"strconv"
	"testing"
)

// appendFloatRef is the oracle appendFloat is pinned to: strconv's shortest 'f' form inside
// [1e-6, 1e21) and at zero, its shortest 'e' form outside, with the exponent unpadded.
func appendFloatRef(dst []byte, f float64) []byte {
	abs := math.Abs(f)
	if (abs >= 1e-6 && abs < 1e21) || abs == 0 {
		return strconv.AppendFloat(dst, f, 'f', -1, 64)
	}
	dst = strconv.AppendFloat(dst, f, 'e', -1, 64)
	// e-07 to e-7; positive exponents outside the range have at least two digits.
	n := len(dst)
	if dst[n-4] == 'e' && dst[n-3] == '-' && dst[n-2] == '0' {
		dst[n-2] = dst[n-1]
		dst = dst[:n-1]
	}
	return dst
}

// diffFloats formats each value and its negation with appendFloat and appendFloatRef and fails on
// the first difference.
func diffFloats(t *testing.T, vals []float64) {
	t.Helper()
	var got, want []byte
	for _, v := range vals {
		for _, f := range [2]float64{v, -v} {
			var err error
			got, err = appendFloat(got[:0], f)
			if err != nil {
				t.Fatalf("appendFloat(%#016x): %v", math.Float64bits(f), err)
			}
			want = appendFloatRef(want[:0], f)
			if !bytes.Equal(got, want) {
				t.Fatalf("appendFloat(%#016x) = %q, want %q", math.Float64bits(f), got, want)
			}
		}
	}
}

func TestAppendFloatEdgeCasesMatchStrconv(t *testing.T) {
	bits := math.Float64frombits
	vals := []float64{
		0, bits(1), bits(2), bits(3), bits(0x000fffffffffffff), bits(0x0010000000000000),
		bits(0x0010000000000001), math.MaxFloat64, math.Nextafter(math.MaxFloat64, 0),
		// Ryu's d2s_test.cc regressions and the shortest-mode cases of strconv's tests.
		2.109808898695963e16, 4.940656e-318, 1.18575755e-316, 2.989102097996e-312,
		9.0608011534336e15, 4.708356024711512e18, 9.409340012568248e18, 1.2345678,
		2.9802322387695312e-8, 1.8531501765868567e21, 3.347727380279489e33,
		1.9430376160308388e16, 6.9741824662760956e19, 4.3816050601147837e18,
		1.8014398509481984e16, 498484681984085570, 383260575764816448,
		5.8339553793802237e23, 1.0000000000000002e23, 1e23, 8.41e21, 4.8726570057e288,
		5e-324, 2.2250738585072014e-308, 2.2250738585072011e-308, 100, 1000000, 1e20,
		bits(0x4830F0CF064DD592), bits(0x4840F0CF064DD592), bits(0x4850F0CF064DD592),
	}
	// The exponents Ryu's tests pin for the shortest-interval and length boundaries.
	for _, e := range []struct{ exp, mant uint64 }{
		{4, 0}, {6, 0x000FFFFFFFFFFFFF}, {41, 0}, {40, 0x000FFFFFFFFFFFFF}, {1077, 0},
		{1076, 0x000FFFFFFFFFFFFF}, {307, 0}, {306, 0x000FFFFFFFFFFFFF}, {934, 0x000FA7161A4D6E0C},
	} {
		vals = append(vals, bits(e.exp<<52|e.mant))
	}
	near := func(f float64, k int) {
		lo, hi := f, f
		vals = append(vals, f)
		for range k {
			lo, hi = math.Nextafter(lo, 0), math.Nextafter(hi, math.Inf(1))
			vals = append(vals, lo, hi)
		}
	}
	// Every power of two, where the rounding interval is lopsided, and every power of ten.
	for e := -1074; e <= 1023; e++ {
		near(math.Ldexp(1, e), 2)
	}
	for e := -323; e <= 308; e++ {
		f, err := strconv.ParseFloat("1e"+strconv.Itoa(e), 64)
		if err != nil {
			t.Fatal(err)
		}
		near(f, 3)
	}
	// Integers around 2^53 and 2^54, where neighbours stop being one apart.
	for i := -16; i <= 16; i++ {
		vals = append(vals, float64(1<<53+i), float64(1<<54+2*i))
	}
	// The notation boundaries, and 1e21 to 1e23.
	for _, f := range []float64{1e-6, 1e-7, 1e21, 1e22, 1e23, 9.999999999999999e22, 9.999999999999999e-7} {
		near(f, 8)
	}
	diffFloats(t, vals)
}

// TestAppendFloatShortDecimalsMatchStrconv covers every n / 10^p with n < 10^5 and p <= 8, the
// values whose shortest form is shorter than the precision shortFloat64 scales to and so has the
// most trailing zeros to remove.
func TestAppendFloatShortDecimalsMatchStrconv(t *testing.T) {
	vals := make([]float64, 0, 9*100_000)
	for p := 0; p <= 8; p++ {
		for n := range 100_000 {
			vals = append(vals, float64(n)/math.Pow10(p))
		}
	}
	diffFloats(t, vals)
}

// TestAppendFloatRandomBitsMatchStrconv draws bit patterns uniformly over all finite values, and
// over the exponents of [2^-100, 2^100) where most JSON numbers are, in samples small enough to
// keep the test well under a second.
func TestAppendFloatRandomBitsMatchStrconv(t *testing.T) {
	n := 200_000
	if testing.Short() {
		n = 20_000
	}
	tests := []struct {
		name           string
		seed           uint64
		expLo, expSpan uint64
	}{
		{name: "all_finite", seed: 1, expSpan: 0x7ff},
		{name: "exp_pm100", seed: 2, expLo: 1023 - 100, expSpan: 200},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := rand.New(rand.NewPCG(tt.seed, tt.seed))
			vals := make([]float64, 0, n)
			for range n {
				b := r.Uint64()
				exp := tt.expLo + (b>>52&0x7ff)%tt.expSpan
				vals = append(vals, math.Float64frombits(b&^(0x7ff<<52)|exp<<52))
			}
			diffFloats(t, vals)
		})
	}
}

func TestAppendFloatZeroKeepsSign(t *testing.T) {
	tests := []struct {
		name string
		give float64
		want string
	}{
		{name: "positive", give: 0, want: "0"},
		{name: "negative", give: math.Copysign(0, -1), want: "-0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := appendFloat([]byte("x"), tt.give)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != "x"+tt.want {
				t.Errorf("appendFloat(%v) = %q, want %q", tt.give, got[1:], tt.want)
			}
		})
	}
}

func TestAppendFloatNonFiniteFails(t *testing.T) {
	tests := []struct {
		name string
		give float64
	}{
		{name: "pos_inf", give: math.Inf(1)},
		{name: "neg_inf", give: math.Inf(-1)},
		{name: "nan", give: math.NaN()},
		{name: "nan_payload", give: math.Float64frombits(0xfff0000000000001)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := appendFloat([]byte("x"), tt.give)
			if err == nil || err.Error() != "INF or NaN number found" {
				t.Errorf("appendFloat(%v) error = %v, want INF or NaN number found", tt.give, err)
			}
			if got != nil {
				t.Errorf("appendFloat(%v) = %q, want nil", tt.give, got)
			}
		})
	}
}

// TestTrimZeros checks the multiply-and-rotate divisibility test against plain division, for
// every count of trailing zeros a uint64 can hold.
func TestTrimZeros(t *testing.T) {
	r := rand.New(rand.NewPCG(5, 5))
	for range 100_000 {
		k := r.Uint64N(1e17) + 1
		for k%10 == 0 {
			k /= 10
		}
		for z := 0; z <= 19 && k <= math.MaxUint64/uint64pow10[z]; z++ {
			d, p := trimZeros(k*uint64pow10[z], 7)
			if d != k || p != 7+z {
				t.Fatalf("trimZeros(%d, 7) = %d, %d; want %d, %d", k*uint64pow10[z], d, p, k, 7+z)
			}
		}
	}
}
