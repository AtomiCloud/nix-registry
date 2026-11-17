package bun

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AtomiCloud/obs/internal/contract"
	"github.com/AtomiCloud/obs/internal/gen"
	"github.com/AtomiCloud/obs/internal/gen/gentest"
)

func load(t *testing.T) *contract.Contract {
	t.Helper()
	c, err := contract.LoadValid(gentest.Fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestGolden(t *testing.T) { gentest.Golden(t, "bun") }

func TestDeterministic(t *testing.T) {
	a, _ := Render(load(t))
	b, _ := Render(load(t))
	if a != b {
		t.Fatal("two renders differ")
	}
}

func TestPlantedChangeIsRed(t *testing.T) {
	c := load(t)
	t.Setenv("OBS_UPDATE_GOLDEN", "")
	base, _ := Render(c)
	pt := c.Types["provider"]
	pt.Values = append(pt.Values, "square")
	c.Types["provider"] = pt
	changed, _ := Render(c)
	if base == changed || !strings.Contains(changed, `'square'`) {
		t.Fatal("a contract change did not change the output")
	}
	want, err := os.ReadFile(filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(gentest.Fixture(t)))), "gen", "bun", "src", "signals", "signals.ts"))
	if err != nil {
		t.Skip("golden file not found")
	}
	if string(want) == changed {
		t.Fatal("golden did not notice a planted enum value")
	}
}

func TestLargeNumberGetsBrand(t *testing.T) {
	c := load(t)
	hi := 9999
	ty := c.Types["shard"]
	ty.Max = &hi
	c.Types["shard"] = ty
	s, err := Render(c)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{
		`export type Shard = number & { readonly __durianBounded: 'Shard' };`,
		`export const ShardSpec = { name: 'Shard', min: 0, max: 9999 } as const;`,
	} {
		if !strings.Contains(s, w) {
			t.Fatalf("missing %q in\n%s", w, s)
		}
	}
}

func TestNameCollisionRefused(t *testing.T) {
	c := load(t)
	c.Types["string"] = contract.Type{Kind: contract.KindFree}
	c.TypeOrder = append(c.TypeOrder, "string")
	if _, err := Render(c); err == nil || !strings.Contains(err.Error(), "String") {
		t.Fatalf("want a refusal naming String, got %v", err)
	}
}

func TestSpanAndMetricAreTypeAliases(t *testing.T) {
	s, _ := Render(load(t))
	if strings.Contains(s, "export interface ") {
		t.Fatal("generated code must not use interface")
	}
}

// Review of PR 111, finding 9: every symbol the file emits is in the collision registry.
func TestFixedNamesAreReserved(t *testing.T) {
	for _, id := range []string{"metric_labels", "span_attrs"} {
		c, err := contract.Parse("c.yaml", []byte("service: s\ntypes: { "+id+": { kind: free } }\nmetrics: { m: { kind: counter } }\nspans: { a: {} }\n"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Render(c); err == nil || !strings.Contains(err.Error(), gen.Pascal(id)) {
			t.Fatalf("type %s: want a refusal naming %s, got %v", id, gen.Pascal(id), err)
		}
	}
}

// Review finding 10: the concept's example (free without a declaration) generates a string.
func TestIntrinsicFreeIsString(t *testing.T) {
	c, err := contract.Parse("c.yaml", []byte("service: s\nspans: { checkout.charge: { attributes: { user_id: free } } }\n"))
	if err != nil {
		t.Fatal(err)
	}
	if is := c.Validate(); len(is) > 0 {
		t.Fatal(is)
	}
	s, err := Render(c)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s, "export type Free = string;") || !strings.Contains(s, "user_id?: Free") {
		t.Fatalf("free attribute not generated as a string:\n%s", s)
	}
}
