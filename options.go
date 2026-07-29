// Modifications copyright (c) 2026 DATA.BET, licensed under the Apache License,
// Version 2.0.

package simdjson

// ParserOption is a parser option.
type ParserOption func(pj *internalParsedJson) error

// WithValidateUTF8 will reject input that is not valid UTF-8.
//
// The parser otherwise checks everything else about a string - escapes, surrogate
// pairs, unescaped control characters - but passes malformed UTF-8 through
// untouched, so a caller that treats the result as text can end up with invalid
// sequences it never put there.
//
// Validation is a separate pass over the message. On the benchmark corpus it costs
// about 0.5% of parse time on number-heavy input and up to 19% on input that is
// both string-dense and largely non-ASCII, since the check runs far faster over
// plain ASCII than over multibyte sequences.
//
// Default: false - UTF-8 is not checked, which is how the parser has always
// behaved.
func WithValidateUTF8(b bool) ParserOption {
	return func(pj *internalParsedJson) error {
		pj.validateUTF8 = b
		return nil
	}
}

// WithCopyStrings will copy strings so they no longer reference the input.
// For enhanced performance, simdjson-go can point back into the original JSON buffer for strings,
// however this can lead to issues in streaming use cases scenarios, or scenarios in which
// the underlying JSON buffer is reused. So the default behaviour is to create copies of all
// strings (not just those transformed anyway for unicode escape characters) into the separate
// Strings buffer (at the expense of using more memory and less performance).
// Default: true - strings are copied.
func WithCopyStrings(b bool) ParserOption {
	return func(pj *internalParsedJson) error {
		pj.copyStrings = b
		return nil
	}
}
