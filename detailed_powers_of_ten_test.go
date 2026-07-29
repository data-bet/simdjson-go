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
	"math/big"
	"testing"
)

// TestDetailedPowersOfTen recomputes detailedPowersOfTen from its definition - the
// 128 bit truncation of 10^q normalised so the top bit is set - and compares.
//
// The table came into the repository with the Ryu float formatter in ftoa_ryu.go and
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
