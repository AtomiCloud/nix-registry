package check

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AtomiCloud/obs/internal/files"
)

func TestTraceQLLoadedContractNames(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "contract.yaml"), []byte(`service: checkout
types:
  provider: {kind: enum, values: [stripe, adyen]}
spans:
  checkout.charge:
    kind: internal
    attributes: {provider: provider}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, attr := range []string{"provider", "planted_typo"} {
		q := files.Query{File: "dashboards/traces.json", Where: "panel 1 target A", Kind: files.Traces, Expr: fmt.Sprintf(`{span.%s="stripe"}`, attr)}
		set := &files.Set{Queries: []files.Query{q}, Dashboards: []*files.Dashboard{{}}}
		r := Static(set, Options{Dir: dir})
		if attr == "provider" {
			if r.Failures() != 0 {
				t.Fatalf("green declared name: %+v", r.Findings)
			}
		} else {
			text := fmt.Sprint(r.Findings)
			if r.Failures() != 1 || !strings.Contains(text, "traceql-attribute") || !strings.Contains(text, attr) || !strings.Contains(text, q.File) || !strings.Contains(text, q.Where) {
				t.Fatalf("planted undeclared span attribute: %+v", r.Findings)
			}
		}
	}
}
