package golang

import (
	"strings"
	"testing"

	"github.com/AtomiCloud/obs/internal/contract"
	"github.com/AtomiCloud/obs/internal/gen"
	"github.com/AtomiCloud/obs/internal/gen/gentest"
)

// The golden file is the fixture consumer repo's committed output; tests/typecheck/go.sh compiles it
// the way a consumer uses it (run by tests/run.sh in the nix check).
func TestGolden(t *testing.T) { gentest.Golden(t, "go") }

func TestCollisionRefused(t *testing.T) {
	c, err := contract.Parse("c.yaml", []byte(`
service: s
types: { provider: { kind: enum, values: [a] } }
metrics: { provider: { kind: counter } }
`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gen.Generate("go", c, gen.Options{}); err == nil || !strings.Contains(err.Error(), "Provider") {
		t.Fatalf("want a refusal naming Provider, got %v", err)
	}
}
