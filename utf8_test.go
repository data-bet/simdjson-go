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
	"testing"
)

// utf8Cases covers the classes a UTF-8 checker has to get right: bare
// continuation bytes, truncated sequences, overlong encodings, the surrogate
// range, and code points above U+10FFFF.
var utf8Cases = []struct {
	name  string
	body  string
	valid bool
}{
	{"ascii", "hello", true},
	{"two byte", "é", true},
	{"three byte", "€", true},
	{"four byte", "\U0001f600", true},
	{"max legal", "\U0010ffff", true},
	{"lone continuation", "\x80", false},
	{"continuation run", "\x80\x80", false},
	{"truncated two byte", "\xc3", false},
	{"truncated three byte", "\xe2\x82", false},
	{"truncated four byte", "\xf0\x9f\x98", false},
	{"lead without continuation", "\xc3z", false},
	{"overlong nul", "\xc0\x80", false},
	{"overlong slash", "\xc0\xaf", false},
	{"overlong three byte", "\xe0\x80\xaf", false},
	{"surrogate d800", "\xed\xa0\x80", false},
	{"surrogate dfff", "\xed\xbf\xbf", false},
	{"above max", "\xf4\x90\x80\x80", false},
	{"f5 lead", "\xf5\x80\x80\x80", false},
	{"fe byte", "\xfe", false},
	{"ff byte", "\xff", false},
}

func TestValidateUTF8(t *testing.T) {
	if !SupportedCPU() {
		t.SkipNow()
	}
	for _, tt := range utf8Cases {
		t.Run(tt.name, func(t *testing.T) {
			msg := []byte(fmt.Sprintf(`{"a":"%s"}`, tt.body))

			_, err := Parse(msg, nil, WithValidateUTF8(true))
			if tt.valid && err != nil {
				t.Errorf("Parse(WithValidateUTF8(true)) = %v, want success", err)
			}
			if !tt.valid && err == nil {
				t.Errorf("Parse(WithValidateUTF8(true)) accepted invalid UTF-8 %q", tt.body)
			}

			// Without the option nothing may change, including for the invalid cases:
			// the parser has always let malformed UTF-8 through and callers rely on it.
			if _, err := Parse(msg, nil, WithValidateUTF8(false)); err != nil {
				t.Errorf("Parse(WithValidateUTF8(false)) = %v, want success", err)
			}
			if _, err := Parse(msg, nil); err != nil {
				t.Errorf("Parse(default) = %v, want success", err)
			}
		})
	}
}

func TestValidateUTF8NDJSON(t *testing.T) {
	if !SupportedCPU() {
		t.SkipNow()
	}
	good := []byte("{\"a\":\"é\"}\n{\"b\":\"x\"}")
	bad := []byte("{\"a\":\"x\"}\n{\"b\":\"\x80\"}")

	if _, err := ParseND(good, nil, WithValidateUTF8(true)); err != nil {
		t.Errorf("ParseND(valid) = %v, want success", err)
	}
	if _, err := ParseND(bad, nil, WithValidateUTF8(true)); err == nil {
		t.Error("ParseND accepted invalid UTF-8")
	}
	if _, err := ParseND(bad, nil); err != nil {
		t.Errorf("ParseND(default) = %v, want success", err)
	}
}

// Invalid UTF-8 outside a string is rejected regardless of the option, since such
// a byte is neither structural nor whitespace and starts no valid value.
func TestInvalidUTF8OutsideString(t *testing.T) {
	if !SupportedCPU() {
		t.SkipNow()
	}
	for _, msg := range []string{"{\"a\":\x80}", "[\x80]", "{\x80:1}"} {
		if _, err := Parse([]byte(msg), nil); err == nil {
			t.Errorf("Parse(%q) succeeded, want failure", msg)
		}
	}
}
