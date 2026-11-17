package bun

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AtomiCloud/obs/internal/contract"
	"github.com/AtomiCloud/obs/internal/gen"
)

const (
	fixture = "../../../../tests/fixtures/gen/good/observability/contract.yaml"
	golden  = "../../../../tests/golden/bun/signals.ts.golden"
)

func load(t *testing.T) *contract.Contract {
	t.Helper()
	c, err := contract.LoadValid(fixture)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestGolden(t *testing.T) {
	c := load(t)
	fs, err := gen.Generate("bun", c, gen.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 1 || fs[0].Path != File {
		t.Fatalf("files %v", fs)
	}
	if os.Getenv("OBS_UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, fs[0].Content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != string(fs[0].Content) {
		t.Fatalf("generated signals.ts differs from %s (OBS_UPDATE_GOLDEN=1 rewrites it):\n%s", golden, fs[0].Content)
	}
	if gen.HashOf(fs[0].Content) != c.Hash {
		t.Fatalf("header hash %q != contract hash %q", gen.HashOf(fs[0].Content), c.Hash)
	}
}

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
	if base == changed || !strings.Contains(changed, `"square"`) {
		t.Fatal("a contract change did not change the output")
	}
	want, _ := os.ReadFile(golden)
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
		`export type Shard = number & { readonly __durianBounded: "Shard" };`,
		`export const ShardSpec = { name: "Shard", min: 0, max: 9999 } as const;`,
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
