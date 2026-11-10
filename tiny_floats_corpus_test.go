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
	"strconv"
	"testing"
)

// tokenShape is the kind of a number token in the tiny-floats corpus.
type tokenShape string

const (
	shapeExponent     tokenShape = "exponent"
	shapeLeadingZeros tokenShape = "leading_zeros"
	shapeShort        tokenShape = "short_decimal"
	shapeInteger      tokenShape = "integer"
	shapeOther        tokenShape = "other"
)

// numberTokens returns the number tokens of a JSON document in order. It does not look inside
// strings.
func numberTokens(doc []byte) [][]byte {
	var toks [][]byte
	inStr := false
	for i := 0; i < len(doc); i++ {
		c := doc[i]
		switch {
		case inStr && c == '\\':
			i++
		case c == '"':
			inStr = !inStr
		case !inStr && (c == '-' || c >= '0' && c <= '9'):
			end := i + 1
			for end < len(doc) && bytes.IndexByte([]byte("0123456789.eE+-"), doc[end]) >= 0 {
				end++
			}
			toks = append(toks, doc[i:end])
			i = end - 1
		}
	}
	return toks
}

// numberShape returns the shape of tok. The long shapes have 15 to 17 significant digits,
// because a float64 serializer writes that many, and these tokens cost a parser the most.
func numberShape(tok []byte) tokenShape {
	tok = bytes.TrimPrefix(tok, []byte("-"))
	mant, _, hasExp := bytes.Cut(bytes.ToLower(tok), []byte("e"))
	sig := len(bytes.TrimLeft(bytes.ReplaceAll(mant, []byte("."), nil), "0"))
	long := sig >= 15 && sig <= 17
	switch {
	case hasExp && long:
		return shapeExponent
	case hasExp:
		return shapeOther
	case bytes.IndexByte(mant, '.') < 0:
		return shapeInteger
	case sig <= 3:
		return shapeShort
	case bytes.HasPrefix(mant, []byte("0.0")) && long:
		return shapeLeadingZeros
	default:
		return shapeOther
	}
}

// TestTinyFloatsCorpusMix checks that the corpus has the mix of number shapes that the generator
// makes. Without this test, a changed corpus could stop measuring these shapes, and no test
// would fail.
func TestTinyFloatsCorpusMix(t *testing.T) {
	doc := loadCompressed(t, "tiny-floats")
	toks := numberTokens(doc)
	counts := make(map[tokenShape]int)
	for _, tok := range toks {
		counts[numberShape(tok)]++
	}
	t.Logf("%d bytes, %d number tokens: %v", len(doc), len(toks), counts)

	tests := []struct {
		shape    tokenShape
		min, max float64
	}{
		{shapeExponent, 0.47, 0.53},
		{shapeLeadingZeros, 0.37, 0.43},
		{shapeShort, 0.03, 0.07},
		{shapeInteger, 0.03, 0.07},
		{shapeOther, 0, 0.001},
	}
	for _, tt := range tests {
		share := float64(counts[tt.shape]) / float64(len(toks))
		if share < tt.min || share > tt.max {
			t.Errorf("%s tokens are %.2f%% of %d, want %.1f%% to %.1f%%",
				tt.shape, 100*share, len(toks), 100*tt.min, 100*tt.max)
		}
	}

	edges := []string{
		`"negative_zero":-0.0`,
		`"upper_e":3.141592653589793E-12`,
		`"plus_exponent":6.02214076e+23`,
		`"integer_exponent":1e5`,
		`"max":1.7976931348623157e+308`,
		`"subnormal":4.9406564584124654e-324`,
		`"digits19":9007199254740993.001`,
		`"digits20":9007199254740993.0001`,
	}
	for _, edge := range edges {
		if !bytes.Contains(doc, []byte(edge)) {
			t.Errorf("the edge token %s is missing", edge)
		}
	}
}

// TestTinyFloatsCorpusRoundTrip checks that each number, also each edge shape, gives the same
// float64 after Parse and MarshalJSON.
func TestTinyFloatsCorpusRoundTrip(t *testing.T) {
	if !SupportedCPU() {
		t.SkipNow()
	}
	doc := loadCompressed(t, "tiny-floats")
	pj, err := Parse(doc, nil)
	if err != nil {
		t.Fatal(err)
	}
	it := pj.Iter()
	out, err := it.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	in, got := numberTokens(doc), numberTokens(out)
	if len(got) != len(in) {
		t.Fatalf("MarshalJSON wrote %d number tokens, the input has %d", len(got), len(in))
	}
	for i := range in {
		want, err := strconv.ParseFloat(string(in[i]), 64)
		if err != nil {
			t.Fatalf("input token %q: %v", in[i], err)
		}
		have, err := strconv.ParseFloat(string(got[i]), 64)
		if err != nil {
			t.Fatalf("output token %q for input %q: %v", got[i], in[i], err)
		}
		if math.Float64bits(have) != math.Float64bits(want) {
			t.Fatalf("token %d: %q came back as %q", i, in[i], got[i])
		}
	}
}
