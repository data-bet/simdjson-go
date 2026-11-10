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
	"math"
	"math/rand/v2"
	"strconv"
	"strings"
	"testing"
)

// numberShapeLen is a power of two so that the benchmark loops can index with a mask.
const numberShapeLen = 4096

var (
	numberSinkTag   uint64
	numberSinkBytes []byte
)

// BenchmarkParseNumberShapes times parseNumber over synthetic tokens grouped by shape, because
// each shape takes a different route through the scanner and the conversion.
func BenchmarkParseNumberShapes(b *testing.B) {
	r := rand.New(rand.NewPCG(1, 2))
	shapes := []struct {
		name string
		toks [][]byte
	}{
		{"dec_1_3", decimalTokens(r, 1, 3, 1e-2, 1e4)},
		{"dec_4_8", decimalTokens(r, 4, 8, 1e-2, 1e6)},
		{"dec_15_17", decimalTokens(r, 15, 17, 1e-1, 1e6)},
		{"exp", exponentTokens(r)},
		{"lead_zero", decimalTokens(r, 12, 17, 1e-6, 1e-2)},
		{"int", integerTokens(r)},
	}
	for _, s := range shapes {
		b.Run("shape="+s.name, func(b *testing.B) {
			mask := len(s.toks) - 1
			var acc uint64
			i := 0
			for b.Loop() {
				tag, val := parseNumber(s.toks[i&mask])
				acc += tag ^ val
				i++
			}
			numberSinkTag = acc
		})
	}
}

// BenchmarkAppendFloatShapes times appendFloat over synthetic values grouped by the length of
// their shortest form and by the notation it is written in.
func BenchmarkAppendFloatShapes(b *testing.B) {
	r := rand.New(rand.NewPCG(3, 4))
	shapes := []struct {
		name string
		vals []float64
	}{
		{"dec_1_3", shortestValues(r, 1, 3, 1e-2, 1e4)},
		{"dec_4_8", shortestValues(r, 4, 8, 1e-2, 1e6)},
		{"dec_15_17", shortestValues(r, 15, 17, 1e-1, 1e6)},
		{"integral", integralValues(r)},
		{"exp_small", shortestValues(r, 1, 17, 1e-30, 1e-6)},
		{"exp_large", shortestValues(r, 1, 17, 1e21, 1e30)},
		{"zero", zeroValues()},
	}
	for _, s := range shapes {
		b.Run("shape="+s.name, func(b *testing.B) {
			mask := len(s.vals) - 1
			buf := make([]byte, 0, 64)
			i := 0
			for b.Loop() {
				var err error
				buf, err = appendFloat(buf[:0], s.vals[i&mask])
				if err != nil {
					b.Fatal(err)
				}
				i++
			}
			numberSinkBytes = buf
		})
	}
}

// shortestValues draws values of either sign whose shortest form has between minDigits and
// maxDigits significant digits and whose magnitude is log-uniform in [lo, hi).
func shortestValues(r *rand.Rand, minDigits, maxDigits int, lo, hi float64) []float64 {
	vals := make([]float64, 0, numberShapeLen)
	for len(vals) < numberShapeLen {
		vals = append(vals, shortestValue(r, minDigits, maxDigits, lo, hi))
	}
	return vals
}

func shortestValue(r *rand.Rand, minDigits, maxDigits int, lo, hi float64) float64 {
	logLo, logHi := math.Log10(lo), math.Log10(hi)
	digits := make([]byte, 0, 17)
	for {
		nd := minDigits + r.IntN(maxDigits-minDigits+1)
		digits = append(digits[:0], byte('1'+r.IntN(9)))
		for len(digits) < nd {
			digits = append(digits, byte('0'+r.IntN(10)))
		}
		if nd > 1 {
			digits[nd-1] = byte('1' + r.IntN(9))
		}
		mag := int(math.Floor(logLo + r.Float64()*(logHi-logLo)))
		f, err := strconv.ParseFloat(string(digits)+"e"+strconv.Itoa(mag-nd+1), 64)
		if err != nil || f < lo || f >= hi || shortestDigits(f) != nd {
			continue
		}
		if r.IntN(2) == 0 {
			return -f
		}
		return f
	}
}

// shortestDigits returns the number of significant digits in the shortest form of f.
func shortestDigits(f float64) int {
	s := strconv.FormatFloat(f, 'e', -1, 64)
	mant := s[:strings.IndexByte(s, 'e')]
	return len(strings.Replace(strings.TrimPrefix(mant, "-"), ".", "", 1))
}

func integralValues(r *rand.Rand) []float64 {
	vals := make([]float64, 0, numberShapeLen)
	for len(vals) < numberShapeLen {
		vals = append(vals, float64(r.Int64N(1e15))-5e14)
	}
	return vals
}

func zeroValues() []float64 {
	vals := make([]float64, numberShapeLen)
	for i := 1; i < len(vals); i += 2 {
		vals[i] = math.Copysign(0, -1)
	}
	return vals
}

// decimalTokens writes values from shortestValue in plain decimal notation, keeping the ones
// that have a decimal point so that every token is a float token.
func decimalTokens(r *rand.Rand, minDigits, maxDigits int, lo, hi float64) [][]byte {
	toks := make([][]byte, 0, numberShapeLen)
	for len(toks) < numberShapeLen {
		tok := strconv.AppendFloat(nil, shortestValue(r, minDigits, maxDigits, lo, hi), 'f', -1, 64)
		if strings.IndexByte(string(tok), '.') < 0 {
			continue
		}
		toks = append(toks, append(tok, ','))
	}
	return toks
}

// exponentTokens writes values from 1e-30 to 1e30 in exponent notation with either case of the
// exponent marker, the way JSON encoders of other languages tend to.
func exponentTokens(r *rand.Rand) [][]byte {
	vals := shortestValues(r, 1, 17, 1e-30, 1e30)
	toks := make([][]byte, 0, len(vals))
	for _, f := range vals {
		tok := strconv.AppendFloat(nil, f, 'e', -1, 64)
		if r.IntN(2) == 0 {
			tok = []byte(strings.ToUpper(string(tok)))
		}
		toks = append(toks, append(tok, ','))
	}
	return toks
}

func integerTokens(r *rand.Rand) [][]byte {
	toks := make([][]byte, 0, numberShapeLen)
	for len(toks) < numberShapeLen {
		n := r.Int64N(int64(math.Pow10(1 + r.IntN(18))))
		if r.IntN(2) == 0 {
			n = -n
		}
		toks = append(toks, append(strconv.AppendInt(nil, n, 10), ','))
	}
	return toks
}
