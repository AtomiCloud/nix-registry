package gen

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Words splits a contract name (checkout.charges, user_id, ChargeFailed, a-b) into lowercase words.
func Words(s string) []string {
	var words []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			words = append(words, strings.ToLower(string(cur)))
			cur = cur[:0]
		}
	}
	rs := []rune(s)
	for i, r := range rs {
		switch {
		case r == '.' || r == '_' || r == '-' || r == ' ' || r == '/':
			flush()
		case unicode.IsUpper(r) && i > 0 && (unicode.IsLower(rs[i-1]) || unicode.IsDigit(rs[i-1])):
			flush()
			cur = append(cur, r)
		default:
			cur = append(cur, r)
		}
	}
	flush()
	return words
}

// Pascal makes PascalCase: checkout.charges -> CheckoutCharges. A word that starts with a digit
// keeps it (a leading digit in the whole name is the caller's problem; contract names start with a letter).
func Pascal(s string) string {
	var b strings.Builder
	for _, w := range Words(s) {
		r, n := utf8.DecodeRuneInString(w)
		b.WriteString(strings.ToUpper(string(r)) + w[n:])
	}
	return b.String()
}

// Clean turns every character that cannot be in an identifier into a word break (it's -> it_s,
// <none> -> _none_), so a value such as <none> becomes the words of a safe identifier. The wire value
// is never changed: only the identifier made from it is.
func Clean(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '_' || r == '-' || r == ' ' || r == '/' {
			return r
		}
		return '_'
	}, s)
}

// Camel makes camelCase: user_id -> userId.
func Camel(s string) string {
	p := Pascal(s)
	if p == "" {
		return p
	}
	r, n := utf8.DecodeRuneInString(p)
	return strings.ToLower(string(r)) + p[n:]
}

// Snake makes snake_case: checkout.charges -> checkout_charges.
func Snake(s string) string { return strings.Join(Words(s), "_") }
