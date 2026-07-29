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
	files := []string{"canada", "numbers", "mesh", "mesh.pretty", "marine_ik",
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
