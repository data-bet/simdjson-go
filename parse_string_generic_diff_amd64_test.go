//go:build !noasm && !appengine && gc
// +build !noasm,!appengine,gc

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
	"math/rand"
	"strings"
	"testing"
)

// The portable string parser in parse_string_generic.go has to accept exactly what
// the assembly accepts and produce exactly what it produces, including where the
// assembly is lax - see digitToVal for the case that matters. Nothing on amd64 calls
// it, so this is where the two are held together.
//
// Both routines are driven directly rather than through Parse, so that inputs the
// parser would never reach - a bad escape, a string that runs off the end - are
// covered as well. The copying routine is only run once the scanning one has reported
// a closing quote, because _parse_string has no bound of its own and scans until it
// finds one; stage 2 only calls it under the same guarantee.

// stringDiffInput lays a case out the way stage 2 hands it over: buf starts at the
// opening quote and has enough zeroed padding after it for the assembly to read whole
// 32 byte words past the end of the string.
func stringDiffInput(s string) ([]byte, uint64) {
	buf := make([]byte, len(s), len(s)+128)
	copy(buf, s)
	buf = buf[:cap(buf)]
	return buf, uint64(len(s))
}

// Counted so that a change which makes everything fail on both sides, and so
// compares nothing but rejections, cannot pass unnoticed.
var stringDiffCompared, stringDiffAccepted int

func compareStringParsers(t *testing.T, s string) {
	t.Helper()
	if len(s) == 0 || s[0] != '"' {
		return
	}
	stringDiffCompared++
	buf, maxStringSize := stringDiffInput(s)

	var asmSrc, asmDst uint64
	max := maxStringSize
	asmOK := parseStringSimdValidateOnly(buf, &max, &asmSrc, &asmDst)

	var genSrc, genDst uint64
	max = maxStringSize
	genOK := parseStringValidateOnlyGeneric(buf, &max, &genSrc, &genDst)

	if asmOK != genOK {
		t.Fatalf("%s: scan accepted by assembly=%v, by portable=%v", clip(s), asmOK, genOK)
	}
	if asmOK && (asmSrc != genSrc || asmDst != genDst) {
		t.Fatalf("%s: scan lengths differ: assembly src=%d dst=%d, portable src=%d dst=%d",
			clip(s), asmSrc, asmDst, genSrc, genDst)
	}
	if !asmOK {
		return
	}
	stringDiffAccepted++

	// A closing quote exists, so the copying routine is safe to run. Give it room for
	// the decoded string plus the 32 byte overshoot _parse_string writes.
	room := int(asmDst) + 64
	asmBuf := make([]byte, 0, room)
	genBuf := make([]byte, 0, room)

	asmN, asmCopyOK := parseStringSimd(buf, asmBuf)
	genN, genCopyOK := parseStringGeneric(buf, genBuf)

	if asmCopyOK != genCopyOK {
		t.Fatalf("%s: copy accepted by assembly=%v, by portable=%v", clip(s), asmCopyOK, genCopyOK)
	}
	if !asmCopyOK {
		return
	}
	if asmN != genN {
		t.Fatalf("%s: copy wrote %d bytes in assembly, %d in portable", clip(s), asmN, genN)
	}
	if !bytes.Equal(asmBuf[:asmN], genBuf[:genN]) {
		t.Fatalf("%s: copy produced %q in assembly, %q in portable",
			clip(s), asmBuf[:asmN], genBuf[:genN])
	}
	// The scan and the copy have to agree with each other as well, which is what
	// makes a matching pair of wrong answers show up.
	if asmN != asmDst {
		t.Fatalf("%s: scan says %d decoded bytes, copy wrote %d", clip(s), asmDst, asmN)
	}
}

func TestGenericStringParserMatchesAsm(t *testing.T) {
	if !SupportedCPU() {
		t.SkipNow()
	}

	cases := stringDiffCases()

	// The escapes the assembly's own tables decide, exercised one byte value at a
	// time. This is where the two implementations are most likely to part company,
	// since both tables had to be transcribed out of the data section.
	for c := 0; c < 256; c++ {
		b := byte(c)
		if b == '"' || b == '\\' {
			continue
		}
		cases = append(cases,
			`"\`+string(b)+`"`,
			`"a\`+string(b)+`b"`,
			`"\u00`+string(b)+string(b)+`"`,
			`"\u`+string(b)+`000"`,
			`"\u0`+string(b)+`00"`,
			`"\ud83d\u`+string(b)+`e00"`,
		)
	}

	// Every offset of the closing quote within and just past a 32 byte word, with an
	// escape sitting at each one, since the assembly's room check is derived from
	// where the next quote falls inside the word it has in hand.
	for n := 0; n <= 72; n++ {
		body := strings.Repeat("a", n)
		cases = append(cases,
			`"`+body+`A"`,
			`"`+body+`😀"`,
			`"`+body+`\u00"`,     // the digits run past the closing quote
			`"`+body+`\ud83d\u"`, // the low half runs past the closing quote
			`"`+body+`\u004"`,
			`"A`+body+`"`,
		)
	}

	// The room check refuses an escape whose digits would run past the closing quote:
	// six bytes for \uXXXX and twelve for a surrogate pair. Truncating a complete
	// escape one byte at a time puts the quote at every distance either threshold
	// could be compared against, which is the only way the boundary itself is pinned
	// rather than just the two sides of it.
	// Spelled out from parts so that no tool between here and the file can turn a
	// backslash-u into the character it denotes.
	shortEsc := `\` + `u0041`
	surrogateEsc := `\` + `ud83d` + `\` + `ude00`
	for _, whole := range []string{shortEsc, surrogateEsc} {
		for k := 1; k <= len(whole); k++ {
			for _, lead := range []string{"", "a", strings.Repeat("a", 17), strings.Repeat("a", 30), strings.Repeat("a", 33)} {
				cases = append(cases, `"`+lead+whole[:k]+`"`)
			}
		}
	}

	rng := rand.New(rand.NewSource(13))
	alphabet := []byte(`"\/ubnrtfxAZ09{}[],: ` + "\x00\x01\x7f\xc3\xa9")
	for trial := 0; trial < 20000; trial++ {
		n := rng.Intn(40)
		b := make([]byte, 0, n+2)
		b = append(b, '"')
		for i := 0; i < n; i++ {
			b = append(b, alphabet[rng.Intn(len(alphabet))])
		}
		if trial%2 == 0 {
			b = append(b, '"')
		}
		cases = append(cases, string(b))
	}

	before := stringDiffAccepted
	for _, c := range cases {
		compareStringParsers(t, c)
	}
	compared, accepted := stringDiffCompared, stringDiffAccepted-before
	t.Logf("%d cases, %d compared, %d accepted by both", len(cases), compared, accepted)
	if accepted < compared/4 {
		t.Errorf("only %d of %d compared cases held a string at all; this is mostly comparing rejections", accepted, compared)
	}
}

// Every string in the corpus, taken as stage 2 would hand it over.
func TestGenericStringParserMatchesAsmOnCorpus(t *testing.T) {
	if !SupportedCPU() {
		t.SkipNow()
	}
	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			msg := loadCompressed(t, tt.name)
			n := 0
			for i := 0; i < len(msg); i++ {
				if msg[i] != '"' {
					continue
				}
				end := i + 4096
				if end > len(msg) {
					end = len(msg)
				}
				compareStringParsers(t, string(msg[i:end]))
				n++
			}
			if n == 0 {
				t.Skip("no quotes in this file")
			}
			t.Logf("%d quote positions", n)
		})
	}
}
