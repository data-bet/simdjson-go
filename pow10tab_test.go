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

// TestPow10Tab recomputes every entry of pow10Tab from its definition - 10^p scaled by the power
// of two that puts it in [2^127, 2^128), rounded up - and compares it with what the table holds
// once the hi<<64 - lo form is undone.
func TestPow10Tab(t *testing.T) {
	if got, want := len(pow10Tab), pow10Max-pow10Min+1; got != want {
		t.Fatalf("pow10Tab has %d entries, want %d", got, want)
	}
	one := big.NewInt(1)
	lo128 := new(big.Int).Lsh(one, 127)
	hi128 := new(big.Int).Lsh(one, 128)
	ten := big.NewInt(10)

	for p := pow10Min; p <= pow10Max; p++ {
		num, den := big.NewInt(1), big.NewInt(1)
		if p >= 0 {
			num.Exp(ten, big.NewInt(int64(p)), nil)
		} else {
			den.Exp(ten, big.NewInt(int64(-p)), nil)
		}
		// The shift that puts num/den in [2^127, 2^128), up to the one-off error of comparing
		// bit lengths, which the loop corrects.
		for k := 127 - (num.BitLen() - den.BitLen()); ; k++ {
			n, d := new(big.Int).Set(num), new(big.Int).Set(den)
			if k >= 0 {
				n.Lsh(n, uint(k))
			} else {
				d.Lsh(d, uint(-k))
			}
			// The floor is in [2^127, 2^128) exactly when the value is.
			q, r := new(big.Int).QuoRem(n, d, new(big.Int))
			if q.Cmp(lo128) < 0 {
				continue
			}
			if q.Cmp(hi128) >= 0 {
				t.Fatalf("1e%d: no shift puts the mantissa in [2^127, 2^128)", p)
			}
			if r.Sign() != 0 {
				q.Add(q, one)
			}
			e := pow10Tab[p-pow10Min]
			got := new(big.Int).Lsh(new(big.Int).SetUint64(e.hi), 64)
			got.Sub(got, new(big.Int).SetUint64(e.lo))
			if got.Cmp(q) != 0 {
				t.Errorf("1e%d: table holds {%#016x, %#016x} = %#x, want %#x", p, e.hi, e.lo, got, q)
			}
			break
		}
	}
}
