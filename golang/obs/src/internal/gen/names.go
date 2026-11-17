package gen

import (
	"strings"
	"unicode"
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
		b.WriteString(strings.ToUpper(w[:1]) + w[1:])
	}
	return b.String()
}

// Camel makes camelCase: user_id -> userId.
func Camel(s string) string {
	p := Pascal(s)
	if p == "" {
		return p
	}
	return strings.ToLower(p[:1]) + p[1:]
}

// Snake makes snake_case: checkout.charges -> checkout_charges.
func Snake(s string) string { return strings.Join(Words(s), "_") }
