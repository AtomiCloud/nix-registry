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

// Review of PR 111, finding 9: helper symbols are in the collision registry, and refused before any write.
func TestHelperSymbolCollisionRefused(t *testing.T) {
	for name, doc := range map[string]string{
		"constructor": "service: s\ntypes: { shard: { kind: int, min: 0, max: 3 }, new_shard: { kind: enum, values: [a] } }\n",
		"bound":       "service: s\ntypes: { shard: { kind: int, min: 0, max: 3 }, shard_min: { kind: free } }\n",
		"table":       "service: s\ntypes: { events: { kind: enum, values: [a] } }\nlogs: { A: { event: a.b, level: info } }\n",
		"base":        "service: s\nmetrics: { durian_signal_gauge: { kind: gauge, per_pod: true } }\n",
	} {
		c, err := contract.Parse("c.yaml", []byte(doc))
		if err != nil {
			t.Fatal(err)
		}
		if is := c.Validate(); len(is) > 0 {
			t.Fatalf("%s: contract invalid: %v", name, is)
		}
		if _, err := gen.Generate("go", c, gen.Options{}); err == nil || !strings.Contains(err.Error(), "twice") {
			t.Fatalf("%s: want a collision refusal, got %v", name, err)
		}
	}
}

// Review finding 10: free without a declaration is a string field.
func TestIntrinsicFreeIsString(t *testing.T) {
	c, err := contract.Parse("c.yaml", []byte("service: s\nspans: { checkout.charge: { attributes: { user_id: free } } }\n"))
	if err != nil {
		t.Fatal(err)
	}
	fs, err := gen.Generate("go", c, gen.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(fs[0].Content), "UserId string `obs:\"user_id\"`") {
		t.Fatalf("free attribute is not a string field:\n%s", fs[0].Content)
	}
}
