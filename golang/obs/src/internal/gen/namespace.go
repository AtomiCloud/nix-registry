package gen

import (
	"fmt"
	"strings"
	"unicode"
)

// C# reserved keywords need a leading @ when used as namespace identifiers.
var csharpNamespaceKeywords = func() map[string]bool {
	out := map[string]bool{}
	for _, keyword := range strings.Fields("abstract as base bool break byte case catch char checked class const continue decimal default delegate do double else enum event explicit extern false finally fixed float for foreach goto if implicit in int interface internal is lock long namespace new null object operator out override params private protected public readonly ref return sbyte sealed short sizeof stackalloc static string struct switch this throw true try typeof uint ulong unchecked unsafe ushort using virtual void volatile while __arglist __makeref __reftype __refvalue") {
		out[keyword] = true
	}
	return out
}()

// CanonicalDotnetNamespace accepts dotted C# identifiers without whitespace around
// dots. Escaping is per segment; formatting characters are ignored by C# identity.
func CanonicalDotnetNamespace(namespace string) (string, error) {
	fail := func() (string, error) {
		return "", fmt.Errorf("namespace: %q must be dotted C# identifiers without spaces around dots", namespace)
	}
	var canonical []string
	for _, segment := range strings.Split(namespace, ".") {
		escaped := strings.HasPrefix(segment, "@")
		segment = strings.TrimPrefix(segment, "@")
		if segment == "" {
			return fail()
		}
		for index, r := range []rune(segment) {
			letter := r == '_' || unicode.IsLetter(r) || unicode.Is(unicode.Nl, r)
			rest := letter || unicode.IsDigit(r) || unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Mc, r) || unicode.Is(unicode.Pc, r) || unicode.Is(unicode.Cf, r)
			if (index == 0 && !letter) || (index > 0 && !rest) {
				return fail()
			}
		}
		segment = strings.Map(func(r rune) rune {
			if unicode.Is(unicode.Cf, r) {
				return -1
			}
			return r
		}, segment)
		if !escaped && csharpNamespaceKeywords[segment] {
			return fail()
		}
		canonical = append(canonical, segment)
	}
	return strings.Join(canonical, "."), nil
}
