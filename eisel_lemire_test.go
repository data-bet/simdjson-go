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
	"fmt"
	"math"
	"math/big"
	"math/rand"
	"strconv"
	"testing"
)

// TestDetailedPowersOfTen recomputes detailedPowersOfTen from its definition - the
// 128 bit truncation of 10^q normalised so the top bit is set - and compares.
//
// The table came into the repository with the Ryu float formatter in ftoaryu.go and
// nothing checked it; eiselLemire64 now leans on it too, so it is worth pinning.
func TestDetailedPowersOfTen(t *testing.T) {
	one := big.NewInt(1)
	lo128 := new(big.Int).Lsh(one, 127)
	hi128 := new(big.Int).Lsh(one, 128)
	mask64 := new(big.Int).Sub(new(big.Int).Lsh(one, 64), one)

	for q := detailedPowersOfTenMinExp10; q <= detailedPowersOfTenMaxExp10; q++ {
		num, den := new(big.Int).Set(one), new(big.Int).Set(one)
		p := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(abs(q))), nil)
		if q >= 0 {
			num.Set(p)
		} else {
			den.Set(p)
		}

		// Find the unique shift that puts the quotient in [2^127, 2^128).
		m := new(big.Int)
		for k := 127 - (num.BitLen() - den.BitLen()); ; k++ {
			n := new(big.Int).Set(num)
			d := new(big.Int).Set(den)
			if k >= 0 {
				n.Lsh(n, uint(k))
			} else {
				d.Lsh(d, uint(-k))
			}
			m.Div(n, d)
			if m.Cmp(lo128) >= 0 && m.Cmp(hi128) < 0 {
				break
			}
			if m.Cmp(hi128) >= 0 {
				t.Fatalf("1e%d: search overshot", q)
			}
		}

		wantLo := new(big.Int).And(m, mask64).Uint64()
		wantHi := new(big.Int).Rsh(m, 64).Uint64()
		got := detailedPowersOfTen[q-detailedPowersOfTenMinExp10]
		if got[0] != wantLo || got[1] != wantHi {
			t.Fatalf("1e%d: table has {%#016x, %#016x}, want {%#016x, %#016x}",
				q, got[0], got[1], wantLo, wantHi)
		}
	}
}

func abs(i int) int {
	if i < 0 {
		return -i
	}
	return i
}

// TestEiselLemireAgainstStrconv drives eiselLemire64 directly so that the whole
// table range is covered, not only the part parseNumber can reach. Whenever it
// commits to an answer, that answer has to be the one strconv arrives at.
func TestEiselLemireAgainstStrconv(t *testing.T) {
	check := func(man uint64, exp10 int, neg bool) {
		t.Helper()
		got, ok := eiselLemire64(man, exp10, neg)
		if !ok {
			return // declined; the caller falls back to strconv
		}
		s := fmt.Sprintf("%de%d", man, exp10)
		if neg {
			s = "-" + s
		}
		want, err := strconv.ParseFloat(s, 64)
		if err != nil {
			t.Fatalf("%s: strconv refused it: %v", s, err)
		}
		if math.Float64bits(got) != math.Float64bits(want) {
			t.Fatalf("%s: got %v (%#016x), want %v (%#016x)",
				s, got, math.Float64bits(got), want, math.Float64bits(want))
		}
	}

	// Every exponent in the table, with mantissas that stress the rounding: powers of
	// two, all-ones, and the 19 digit extremes.
	for exp10 := detailedPowersOfTenMinExp10; exp10 <= detailedPowersOfTenMaxExp10; exp10++ {
		for _, man := range []uint64{
			1, 2, 3, 5, 9, 10, 1 << 20, 1<<53 - 1, 1 << 53, 1<<53 + 1,
			1<<63 - 1, 1234567890123456789, 9999999999999999999,
			4503599627370495, 4503599627370496, 4503599627370497,
		} {
			check(man, exp10, false)
			check(man, exp10, true)
		}
	}

	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 500000; i++ {
		man := rng.Uint64()
		if man == 0 {
			continue
		}
		// Mostly stay inside 19 digits, which is what the caller can produce.
		if i%2 == 0 {
			man %= 10000000000000000000
			if man == 0 {
				continue
			}
		}
		exp10 := detailedPowersOfTenMinExp10 + rng.Intn(detailedPowersOfTenMaxExp10-detailedPowersOfTenMinExp10+1)
		check(man, exp10, i%3 == 0)
	}
}

// The values that made canada slow: 17 significant digits, which overflow the
// significand and so cannot be done by plain float64 arithmetic.
func TestEiselLemireLongMantissas(t *testing.T) {
	for _, s := range []string{
		"-65.613616999999977", "43.420273000000009", "-65.619720000000029",
		"43.418052999999986", "-65.636123999999882", "43.394821000000021",
		"1.7976931348623157e308", "2.2250738585072014e-308",
		"1.00000000000000011102230246251565404236316680908203125",
	} {
		want, err := strconv.ParseFloat(s, 64)
		if err != nil {
			t.Fatal(err)
		}
		id, val := parseNumber([]byte(s + ","))
		if Tag(id>>JSONTAGOFFSET) != TagFloat {
			t.Errorf("%s: tag = %v, want float", s, Tag(id>>JSONTAGOFFSET))
			continue
		}
		if val != math.Float64bits(want) {
			t.Errorf("%s: got %v, want %v", s, math.Float64frombits(val), want)
		}
	}
}
