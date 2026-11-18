package all_test

import (
	"strings"
	"testing"

	"github.com/AtomiCloud/obs/internal/contract"
	"github.com/AtomiCloud/obs/internal/gen"
)

func parseOK(t *testing.T, doc string) *contract.Contract {
	t.Helper()
	c, err := contract.Parse("apps/s/observability/contract.yaml", []byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if is := c.Validate(); len(is) > 0 {
		t.Fatalf("contract invalid: %v", is)
	}
	return c
}

// An enum value with characters that are not in an identifier ('<none>' is what a scraped label says for
// "no value") becomes a safe identifier in every language, and the wire value stays exactly as written.
func TestPunctuationEnumValueEveryLanguage(t *testing.T) {
	c := parseOK(t, "service: s\ntypes:\n  t: { kind: enum, values: [\"<none>\", \"<unknown>\", \"(n/a)\", \"it's\", \"100%\", \"a b\", \"x:y\", \"<>\", ok] }\nmetrics:\n  m: { kind: counter, budget: 100, labels: { kind: t } }\n")
	for _, lang := range gen.Langs() {
		fs, err := gen.Generate(lang, c, gen.Options{Package: "fuzz", Namespace: "Fuzz.Signals"})
		if err != nil {
			t.Errorf("%s: %v", lang, err)
			continue
		}
		var src strings.Builder
		for _, f := range fs {
			src.Write(f.Content)
		}
		for _, wire := range []string{"<none>", "<unknown>", "(n/a)", "it's", "100%"} {
			if !strings.Contains(src.String(), wire) && !strings.Contains(src.String(), strings.ReplaceAll(wire, "'", "\\'")) && !strings.Contains(src.String(), strings.ReplaceAll(wire, "'", "''")) {
				t.Errorf("%s: the wire value %q is not in the generated code", lang, wire)
			}
		}
	}
}

// Two values that make one identifier are refused naming both values and the contract file:line.
func TestPunctuationEnumCollisionNamesFileAndLine(t *testing.T) {
	c := parseOK(t, "service: s\ntypes:\n  t: { kind: enum, values: [none, \"<none>\"] }\nmetrics:\n  m: { kind: counter, budget: 100, labels: { kind: t } }\n")
	for _, lang := range []string{"go", "dart", "dotnet"} {
		_, err := gen.Generate(lang, c, gen.Options{Package: "fuzz", Namespace: "Fuzz.Signals"})
		if err == nil {
			t.Errorf("%s: none and <none> make one identifier: want a refusal", lang)
			continue
		}
		for _, want := range []string{`"none"`, `"<none>"`, "apps/s/observability/contract.yaml:3"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: the refusal lacks %s: %v", lang, want, err)
			}
		}
	}
	// Bun quotes every value, so the two stay apart there: no refusal needed.
	if _, err := gen.Generate("bun", c, gen.Options{}); err != nil {
		t.Errorf("bun: %v", err)
	}
}
