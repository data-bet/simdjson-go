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
	"fmt"
	"strings"
	"testing"
)

// Strings take one of two routes through parseString. With WithCopyStrings the
// decode copies and finds the closing quote in a single pass; without it the string
// is scanned first, so that one that needs no decoding can be left in the message.
// The two therefore run different assembly over the same bytes and have to agree
// exactly - on what they accept as well as on what they produce.
//
// This is the check that stands behind fusing the copy into the scan: the fused
// routine is now the only thing validating an escape sequence on the copying path.
func stringDiffCases() []string {
	cases := []string{
		`""`, `"a"`, `"ab"`, `"\""`, `"\\"`, `"\/"`, `"\b\f\n\r\t"`,
		`"\u0041"`, `"é"`, `"€"`, `"😀"`, `"\ud83d\ude00"`,
		// Raw control characters may not appear unescaped and have to be rejected.
		"\"\x00\"", "\"\x01\"", "\"\x1f\"", "\"a\x00b\"", "\"a\x1fb\"",
		// Rejected: bad escapes, lone or reversed surrogates, short sequences.
		`"\x"`, `"\u"`, `"\u12"`, `"\u123"`, `"\uZZZZ"`, `"\u12g4"`,
		`"\ud83d"`, `"\ude00"`, `"\ud83dx"`, `"\ude00\ud83d"`, `"\ud83dA"`,
		`"\`, `"abc`, `"abc\"`, `"a` + "\n" + `b"`, `"a` + "\x01" + `b"`,
		// The trailing backslash cases: the last one escapes the closing quote.
		`"\\"`, `"\\\\"`, `"\\\""`,
	}

	// Lengths either side of the 32 byte word the assembly works in, so the closing
	// quote lands at every offset within one, both plain and just past an escape.
	for n := 0; n <= 70; n++ {
		body := strings.Repeat("a", n)
		cases = append(cases, `"`+body+`"`)
		cases = append(cases, `"`+body+`\n"`)
		cases = append(cases, `"\n`+body+`"`)
		cases = append(cases, `"`+body+`é"`)
		cases = append(cases, `"`+body+`\`) // unterminated after the escape
	}

	// Wrap each in the shapes stage 2 reaches parseString from, and leave some bare
	// so that a string ending at the very end of the message - the padded path -
	// is covered too.
	var out []string
	for _, c := range cases {
		out = append(out,
			c,
			`[`+c+`]`,
			`{`+c+`:1}`,
			`{"k":`+c+`}`,
			`[`+c+`,`+c+`]`,
			`[1,`+c+`]`,
			`  [ `+c+` ] `,
		)
	}
	return out
}

func TestStringPathsAgree(t *testing.T) {
	if !SupportedCPU() {
		t.SkipNow()
	}
	cases := stringDiffCases()
	agreed, accepted := 0, 0
	for _, js := range cases {
		msg := []byte(js)

		var out [2][]byte
		var errs [2]error
		for i, copyStrings := range []bool{false, true} {
			pj := &internalParsedJson{}
			pj.copyStrings = copyStrings
			if errs[i] = pj.parseMessage(msg, false); errs[i] != nil {
				continue
			}
			it := pj.ParsedJson.Iter()
			out[i], errs[i] = it.MarshalJSON()
		}

		if (errs[0] == nil) != (errs[1] == nil) {
			t.Fatalf("%s: scan path err=%v, fused path err=%v", clip(js), errs[0], errs[1])
		}
		if errs[0] != nil {
			agreed++
			continue
		}
		if !bytes.Equal(out[0], out[1]) {
			t.Fatalf("%s: scan path produced %s, fused path %s", clip(js), clip(string(out[0])), clip(string(out[1])))
		}
		agreed++
		accepted++
	}
	t.Logf("%d cases, %d agreed, %d accepted by both", len(cases), agreed, accepted)
	if accepted < len(cases)/4 {
		t.Errorf("only %d of %d cases parsed at all; the comparison is mostly comparing rejections", accepted, len(cases))
	}
}

// The fused path is only taken when stage 1 has already produced the structural
// index that follows the string, so it needs a document long enough for strings to
// sit well inside an index buffer rather than at its edge. Run the same comparison
// over the corpus, which is where that holds.
func TestStringPathsAgreeOnCorpus(t *testing.T) {
	if !SupportedCPU() {
		t.SkipNow()
	}
	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			msg := loadCompressed(t, tt.name)
			var out [2][]byte
			for i, copyStrings := range []bool{false, true} {
				pj := &internalParsedJson{}
				pj.copyStrings = copyStrings
				if err := pj.parseMessage(msg, false); err != nil {
					t.Fatalf("copyStrings=%v: %v", copyStrings, err)
				}
				it := pj.ParsedJson.Iter()
				var err error
				if out[i], err = it.MarshalJSON(); err != nil {
					t.Fatalf("copyStrings=%v: MarshalJSON: %v", copyStrings, err)
				}
			}
			if !bytes.Equal(out[0], out[1]) {
				t.Errorf("the two string paths disagree on %s", tt.name)
			}
		})
	}
}

func clip(s string) string {
	if len(s) > 48 {
		return fmt.Sprintf("%q...(%d bytes)", s[:48], len(s))
	}
	return fmt.Sprintf("%q", s)
}
