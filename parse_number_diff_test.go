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
	"errors"
	"fmt"
	"math"
	"math/rand"
	"strconv"
	"strings"
	"testing"
)

// parseNumberRef is parseNumber as it stood before fastInt and fastFloat were
// added: every value went through strconv. It is kept here as the oracle those
// direct paths are pinned to - they may only ever be faster, never different. If
// parseNumber's accepted syntax or returned tags are meant to change, this
// reference has to be updated deliberately, and the diff tests below will say so.
func parseNumberRef(buf []byte) (id, val uint64) {
	pos := 0
	found := uint8(0)
	for i, v := range buf {
		t := isNumberRune[v]
		if t == 0 {
			return 0, 0
		}
		if t == isEOVFlag {
			break
		}
		if t&isMustHaveDigitNext > 0 {
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

	if found&isFloatOnlyFlag == 0 && pos <= maxIntLen {
		if found&isMinusFlag == 0 {
			if pos > 1 && buf[0] == '0' {
				return 0, 0
			}
		} else {
			if pos > 2 && buf[1] == '0' {
				return 0, 0
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
		return 0, 0
	}
	f64, err := strconv.ParseFloat(unsafeBytesToString(buf[:pos]), 64)
	if err == nil {
		return floatTag, math.Float64bits(f64)
	}
	return 0, 0
}

func diffOne(t *testing.T, tok string, checked *int) {
	t.Helper()
	for _, suffix := range []string{",", "", "}", " ", ":"} {
		buf := []byte(tok + suffix)
		gotID, gotVal := parseNumber(buf)
		wantID, wantVal := parseNumberRef(buf)
		*checked++
		if gotID != wantID || gotVal != wantVal {
			gt, wt := Tag(gotID>>JSONTAGOFFSET), Tag(wantID>>JSONTAGOFFSET)
			t.Fatalf("parseNumber(%q):\n got  tag=%v flags=%d val=%d (%v)\n want tag=%v flags=%d val=%d (%v)",
				buf, gt, gotID&JSONVALUEMASK, gotVal, math.Float64frombits(gotVal),
				wt, wantID&JSONVALUEMASK, wantVal, math.Float64frombits(wantVal))
		}
	}
}

func TestParseNumberDiffCorpus(t *testing.T) {
	checked := 0
	fixed := []string{
		"0", "-0", "1", "-1", "00", "01", "-01", "1.0", "0.0", "-0.0", "0.001", "-0.001",
		"1e0", "1E0", "1e+0", "1e-0", "1e", "1e+", "1e-", "1.", ".5", "-.5", "1..2", "1.2.3",
		"1e5e5", "1-2", "1+2", "+1", "--1", "-", "+", "e5", "E5", "",
		"9223372036854775807", "9223372036854775808", "-9223372036854775808",
		"-9223372036854775809", "18446744073709551615", "18446744073709551616",
		"99999999999999999999", "999999999999999999999",
		"1e308", "1e309", "1e-308", "1e-309", "5e-324", "1e-400", "1e400",
		"0.1", "0.2", "0.3", "1.7976931348623157e308", "2.2250738585072014e-308",
		"4503599627370496", "4503599627370497", "9007199254740992", "9007199254740993",
		"1000000000000000000000", "1e22", "1e23", "1e-22", "1e-23",
		"123456789012345678901234567890", "0e0", "0e5", "0e-5", "-0e5",
		"1000000000000000.0", "10000000000000000.0", "-65.613616999999977",
		"1.00000000000000011102230246251565404236316680908203125",
	}
	for _, tok := range fixed {
		diffOne(t, tok, &checked)
	}
	t.Logf("fixed cases: %d comparisons", checked)
}

// TestParseNumberDiffFloatEdges pins the float scanner's boundaries: what it hands to strconv,
// where it stops counting digits, and where the result leaves the float64 range.
func TestParseNumberDiffFloatEdges(t *testing.T) {
	checked := 0
	fixed := []string{
		// Malformed shapes the validation loop lets through.
		"1e", "1E", "1e+", "1e-", "1ee5", "1eE5", "1e5e", "1e+-5", "1e-+5", "1e++5", "1.5e5.3",
		"1.2.3", "1..5", "1.5.", "+1.5", "+1e5", "1+5", "1.5+", "1.5-5", "-1.5-", "e", "E", "e5",
		"+e5", "-e5", ".e5", "1.e5", ".5e5", ".5", "-.5e1", "0.0.0", "0e", "-0e+", "1.5e+5+",
		"0x1p5", "1_000.5",
		// Exponents: case, explicit plus, integer-shaped mantissas, leading zeros.
		"1e5", "1E5", "1e+5", "1E+05", "1e-5", "1E-05", "-1e5", "-1E-5", "12e3", "-12E+3",
		"1.5e0", "1.5E-0", "1.5e+0", "0.5e1", "0.0e0", "0.0e999", "-0.0e-999", "00e5", "-00e5",
		"1e0000000000000000000000005", "1e-0000000000000000000000005", "1.5e000",
		// Exponent runs past what an int holds, and past strconv's saturation at 10000.
		"1e99999999999999999999", "1e-99999999999999999999", "0e99999999999999999999",
		"-0e-99999999999999999999", "1e10000", "1e-10000", "1e9999", "1e-9999", "1e100000",
		"0.00000000000000000000000000000000000000000000000001e99999",
		// Saturation decides the value only when the mantissa's own length offsets it.
		"0." + strings.Repeat("0", 10000) + "1e10005", "0." + strings.Repeat("0", 10000) + "1e9999",
		"-0." + strings.Repeat("0", 10000) + "1e100000000",
		// Negative zero stays negative.
		"-0.0", "-0e0", "-0E5", "-0.000e-5", "-0.0e999", "-0e-999",
		// Range: overflow is rejected, underflow is a zero.
		"1e308", "1.7976931348623157e308", "1.7976931348623158e308", "1.7976931348623159e308",
		"-1.7976931348623159e308", "2e308", "1e309", "1e310", "1e311", "9e310", "0.1e312",
		"1e-999", "-1e-999", "1e-345", "1e-346", "1e-324", "1e-325", "5e-324", "-5e-324",
		"2.4703282292062327e-324", "2.4703282292062328e-324", "4.9406564584124654e-324",
		"2.2250738585072011e-308", "2.2250738585072012e-308", "2.2250738585072014e-308",
		"4.4501477170144023e-308", "123456789012345678e-400", "9999999999999999999e-343",
		"9999999999999999999e290", "1844674407370955161e290", "18446744073709551615e290",
		// 19 against 20 significant digits, with and without leading zeros.
		"1234567890123456789.5", "1.234567890123456789", "1.2345678901234567890",
		"0.1234567890123456789", "0.12345678901234567890", "0.0000000000000000001234567890123456789",
		"-0.00000000000000000012345678901234567891", "9999999999999999999.0", "99999999999999999999.0",
		"9999999999999999999e0", "99999999999999999999e0", "0.9999999999999999999e19",
		"1844674407370955161.5", "18446744073709551615.0", "18446744073709551616.0",
		"000000000000000000000.5", "-000000000000000000000.5", "-0000000000000000000000000001",
		"0." + strings.Repeat("0", 400) + "1", "0." + strings.Repeat("0", 400) + "1e400",
		"-0." + strings.Repeat("0", 300) + "12345678901234567e310",
		// Exact halfway points between two float64s, and one digit either side.
		"9007199254740993.0", "9007199254740993e0", "9.007199254740993e15", "9007199254740992.9",
		"9007199254740993.1", "4503599627370496.5", "4503599627370497.5", "2251799813685248.25",
		"1125899906842624.125", "1125899906842624.1250000001", "18014398509481986e0",
		"18014398509481990e0", "36028797018963972.0", "36028797018963980.0",
		// Mantissas that atof64exact cannot take but whose exponent it could.
		"9007199254740993e22", "9007199254740993e-22", "123456789012345678e37", "1e38", "1e-23",
	}
	for _, tok := range fixed {
		diffOne(t, tok, &checked)
	}
	t.Logf("fixed cases: %d comparisons", checked)
}

// TestParseNumberDiffFloatShapes generates float tokens in the shapes JSON encoders write and in
// the ones the scanner has to be careful with.
func TestParseNumberDiffFloatShapes(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	checked := 0
	digits := func(n int) string {
		b := make([]byte, n)
		for i := range b {
			b[i] = byte('0' + r.Intn(10))
		}
		return string(b)
	}
	exponent := func() string {
		return string("eE"[r.Intn(2)]) + []string{"", "+", "-"}[r.Intn(3)] + strconv.Itoa(r.Intn(400))
	}
	for range 20000 {
		// Random float64 values in each notation, shortest and at 17 digits.
		f := math.Float64frombits(r.Uint64())
		if math.IsInf(f, 0) || math.IsNaN(f) {
			f = r.NormFloat64()
		}
		g := r.NormFloat64() * math.Pow(10, float64(r.Intn(40)-20))
		for _, v := range []float64{f, g} {
			for _, notation := range []byte{'e', 'f', 'g'} {
				diffOne(t, strconv.FormatFloat(v, notation, -1, 64), &checked)
				diffOne(t, strings.ToUpper(strconv.FormatFloat(v, notation, 17, 64)), &checked)
			}
		}
		// Integer-shaped mantissas with an exponent.
		diffOne(t, digits(1+r.Intn(21))+exponent(), &checked)
		// Leading-zero fractions, with and without an exponent.
		lead := "0." + strings.Repeat("0", r.Intn(30)) + digits(1+r.Intn(21))
		diffOne(t, lead, &checked)
		diffOne(t, "-"+lead+exponent(), &checked)
		// Exponent digit runs of any length.
		diffOne(t, digits(1+r.Intn(5))+"."+digits(1+r.Intn(5))+"e"+strings.Repeat("0", r.Intn(30))+digits(1+r.Intn(25)), &checked)
		// Exact halfway points between adjacent float64s: (2m+1) * 2^(k-1) near 2^(52+k).
		m := 1<<52 + uint64(r.Int63n(1<<52))
		k := r.Intn(4)
		half := (2*m + 1) << k
		diffOne(t, strconv.FormatUint(half, 10)+".0", &checked)
		diffOne(t, strconv.FormatUint(half, 10)+"e-"+strconv.Itoa(r.Intn(3)), &checked)
	}
	t.Logf("generated cases: %d comparisons", checked)
}

// TestParseFloat64MatchesStrconv drives parseFloat64 directly, over every exponent its table
// serves and past both ends, so that the whole range is covered rather than the part parseNumber
// happens to reach.
func TestParseFloat64MatchesStrconv(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	for p := -360; p <= 330; p++ {
		for range 300 {
			d := uint64(r.Int63n(math.MaxInt64))
			switch r.Intn(3) {
			case 0:
				d %= uint64(math.Pow10(1 + r.Intn(18)))
			case 1:
				d = 1e19 - 1 - d%1000
			}
			neg := r.Intn(2) == 0
			s := strconv.FormatUint(d, 10) + "e" + strconv.Itoa(p)
			if neg {
				s = "-" + s
			}
			want, err := strconv.ParseFloat(s, 64)
			got, ok := parseFloat64(d, p, neg)
			if ok != (err == nil) || math.Float64bits(got) != math.Float64bits(want) {
				t.Fatalf("parseFloat64(%d, %d, %v) = %v (%#016x), %v; strconv.ParseFloat(%q) = %v (%#016x), %v",
					d, p, neg, got, math.Float64bits(got), ok, s, want, math.Float64bits(want), err)
			}
		}
	}
}

func TestParseNumberDiffGenerated(t *testing.T) {
	if testing.Short() {
		t.Skip("long")
	}
	rng := rand.New(rand.NewSource(1))
	checked := 0
	digits := "0123456789"

	// Structured tokens: -?d{1,22}(.d{0,22})?([eE][+-]?d{0,4})?
	for i := 0; i < 400000; i++ {
		tok := ""
		if rng.Intn(2) == 0 {
			tok += "-"
		}
		n := 1 + rng.Intn(22)
		for j := 0; j < n; j++ {
			tok += string(digits[rng.Intn(10)])
		}
		if rng.Intn(2) == 0 {
			tok += "."
			m := rng.Intn(23)
			for j := 0; j < m; j++ {
				tok += string(digits[rng.Intn(10)])
			}
		}
		if rng.Intn(3) == 0 {
			tok += string("eE"[rng.Intn(2)])
			switch rng.Intn(3) {
			case 0:
				tok += "+"
			case 1:
				tok += "-"
			}
			m := rng.Intn(5)
			for j := 0; j < m; j++ {
				tok += string(digits[rng.Intn(10)])
			}
		}
		diffOne(t, tok, &checked)
	}

	// Free-form tokens over the number alphabet, to reach malformed shapes.
	alphabet := []byte("0123456789.eE+-")
	for i := 0; i < 400000; i++ {
		n := 1 + rng.Intn(12)
		b := make([]byte, n)
		for j := range b {
			b[j] = alphabet[rng.Intn(len(alphabet))]
		}
		diffOne(t, string(b), &checked)
	}
	t.Logf("generated cases: %d comparisons", checked)
}

func TestParseNumberDiffTestdata(t *testing.T) {
	if !SupportedCPU() {
		t.SkipNow()
	}
	checked := 0
	files := []string{"canada", "numbers", "mesh", "mesh.pretty", "marine_ik", "tiny-floats",
		"citm_catalog", "random", "twitter", "github_events", "instruments", "apache_builds"}
	for _, f := range files {
		data := loadFile(t, "testdata/"+f+".json.zst")
		before := checked
		inStr := false
		for i := 0; i < len(data); i++ {
			c := data[i]
			if inStr {
				if c == '\\' {
					i++
				} else if c == '"' {
					inStr = false
				}
				continue
			}
			if c == '"' {
				inStr = true
				continue
			}
			if c == '-' || (c >= '0' && c <= '9') {
				gotID, gotVal := parseNumber(data[i:])
				wantID, wantVal := parseNumberRef(data[i:])
				checked++
				if gotID != wantID || gotVal != wantVal {
					end := i + 40
					if end > len(data) {
						end = len(data)
					}
					t.Fatalf("%s at %d (%q): got %d/%d want %d/%d",
						f, i, data[i:end], gotID, gotVal, wantID, wantVal)
				}
				for i < len(data) {
					d := data[i]
					if d == '-' || d == '+' || d == '.' || d == 'e' || d == 'E' || (d >= '0' && d <= '9') {
						i++
						continue
					}
					break
				}
				i--
			}
		}
		fmt.Printf("%-14s %d numbers compared\n", f, checked-before)
	}
	t.Logf("testdata cases: %d comparisons", checked)
}
