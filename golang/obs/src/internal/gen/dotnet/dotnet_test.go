package dotnet

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AtomiCloud/obs/internal/contract"
	"github.com/AtomiCloud/obs/internal/gen"
	"github.com/AtomiCloud/obs/internal/gen/gentest"
)

func TestGolden(t *testing.T) { gentest.Golden(t, "dotnet") }

// gen/stress is a second consumer repo: long names, reserved words, odd enum values.
func TestGoldenStress(t *testing.T) {
	fixtures := filepath.Dir(filepath.Dir(filepath.Dir(gentest.Fixture(t)))) // tests/fixtures/gen
	cpath := filepath.Join(fixtures, "stress", "observability", "contract.yaml")
	c, err := contract.LoadValid(cpath)
	if err != nil {
		t.Fatal(err)
	}
	fs, err := gen.Generate("dotnet", c, gen.Options{Namespace: "Pay.Signals"})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range fs {
		p := filepath.Join(fixtures, "stress", "gen", "dotnet", "Signals", f.Path)
		if os.Getenv("OBS_UPDATE_GOLDEN") == "1" {
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, f.Content, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		have, err := os.ReadFile(p)
		if err != nil || !bytes.Equal(have, f.Content) {
			t.Errorf("%s differs from the generated file (OBS_UPDATE_GOLDEN=1 rewrites it)", p)
		}
	}
	src := string(fs[0].Content)
	for _, want := range []string{
		"public static readonly Region Default = new(\"default\");",
		"public static readonly Region Class = new(\"class\");",
		"public static readonly Region V1xx = new(\"1xx\");",
		"public static readonly Region All_ = new(\"All\");", // All is a member of the class itself
		"public static readonly Region Wire_ = new(\"Wire\");",
		"public static readonly Region Region_ = new(\"region\");", // a member may not take its type's name
		`new("it's")`,
		`new("a$b")`,
		`[property: DurianSignalLabel("k8s.zone")] DurianSignalInt<HourSpec> K8sZone`,
		"namespace Pay.Signals;",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("generated C# lacks %q", want)
		}
	}
}

func fixtureContract(t *testing.T) *contract.Contract {
	t.Helper()
	c, err := contract.LoadValid(gentest.Fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// Every file starts with the standard header and names the contract's hash.
func TestHeader(t *testing.T) {
	c := fixtureContract(t)
	fs, err := gen.Generate("dotnet", c, gen.Options{Namespace: "Checkout.Signals"})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range fs {
		if !strings.HasPrefix(string(f.Content), gen.Header(c, "// ")) {
			t.Errorf("%s does not start with the generated-file header", f.Path)
		}
		if gen.HashOf(f.Content) != c.Hash {
			t.Errorf("%s: header hash %q, contract hash %q", f.Path, gen.HashOf(f.Content), c.Hash)
		}
	}
}

// A planted contract change makes the golden comparison red: the file differs and names the value.
func TestPlantedChangeRed(t *testing.T) {
	cpath := gentest.Fixture(t)
	b, err := os.ReadFile(cpath)
	if err != nil {
		t.Fatal(err)
	}
	golden, err := os.ReadFile(filepath.Join(filepath.Dir(filepath.Dir(cpath)), "gen", "dotnet", "Signals", "Signals.g.cs"))
	if err != nil {
		t.Fatal(err)
	}
	planted := strings.Replace(string(b), "values: [stripe, adyen, paypal]", "values: [stripe, adyen, paypal, venmo]", 1)
	if planted == string(b) {
		t.Fatal("the fixture changed: update the planted edit")
	}
	c, err := contract.Parse(cpath, []byte(planted))
	if err != nil || len(c.Validate()) > 0 {
		t.Fatalf("planted contract invalid: %v %v", err, c.Validate())
	}
	fs, err := gen.Generate("dotnet", c, gen.Options{Namespace: "Checkout.Signals"})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(fs[0].Content, golden) {
		t.Fatal("a planted enum value left the generated file unchanged")
	}
	if !strings.Contains(string(fs[0].Content), `Venmo = new("venmo")`) {
		t.Error("the planted value is not in the generated file")
	}
	if gen.HashOf(fs[0].Content) == gen.HashOf(golden) {
		t.Error("the contract hash in the header did not change")
	}
}

func parseValid(t *testing.T, src string) *contract.Contract {
	t.Helper()
	c, err := contract.Parse("contract.yaml", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if is := c.Validate(); len(is) > 0 {
		t.Fatalf("contract invalid: %v", is)
	}
	return c
}

// Two contract names that make one C# name fail, naming both.
func TestCollisionsNamed(t *testing.T) {
	cases := map[string]struct{ src, want string }{
		"enum values": {
			"service: s\ntypes:\n  kind: { kind: enum, values: [a-b, a_b] }\n",
			`types.kind: "a-b" and "a_b" both make the C# member "AB"`,
		},
		"shadowing a system type": {
			"service: s\ntypes:\n  string: { kind: enum, values: [x] }\n",
			`types.string makes the C# name "String", which would shadow a type the generated code uses`,
		},
		"a base type": {
			"service: s\nmetrics:\n  m: { kind: counter }\ntypes:\n  names: { kind: enum, values: [x] }\n",
			`a generated base type and types.names both make the C# name "Names"`,
		},
		"labels": {
			"service: s\ntypes:\n  t: { kind: enum, values: [x] }\nmetrics:\n  m: { kind: counter, labels: { a.b: t, a_b: t } }\n",
			`metrics.m.labels: "a.b" and "a_b" both make the C# member "AB"`,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c, err := contract.Parse("contract.yaml", []byte(tc.src))
			if err != nil {
				t.Skipf("contract does not parse: %v", err) // a duplicate YAML key is the loader's failure
			}
			_, err = gen.Generate("dotnet", c, gen.Options{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want an error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func render(t *testing.T, doc string) string {
	t.Helper()
	c, err := contract.Parse("c.yaml", []byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if is := c.Validate(); len(is) > 0 {
		t.Fatal(is)
	}
	fs, err := gen.Generate("dotnet", c, gen.Options{Namespace: "Fuzz.Signals"})
	if err != nil {
		t.Fatal(err)
	}
	return string(fs[0].Content)
}

// Round 2, finding 4: a member can not take the name of its enclosing type (CS0542).
func TestMembersAvoidTheirEnclosingType(t *testing.T) {
	out := render(t, `
service: s
types: { t: { kind: enum, values: [x] } }
metrics:
  names: { kind: counter }
  m: { kind: counter, budget: 1, labels: { m_labels: t } }
logs: { Events: { event: s.events, level: info } }
spans: { spans: { attributes: { spans_attributes: t } } }
`)
	for _, want := range []string{
		"public sealed record MLabels(", "MLabels_",
		"public static readonly DurianSignalCounter<NamesLabels> Names_ =",
		"public static readonly DurianSignalEvent<DurianSignalLevelInfo> Events_ =",
		"public static readonly DurianSignalSpan<SpansAttributes> Spans_ =",
		"SpansAttributes_",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("generated C# lacks %q", want)
		}
	}
	for _, bad := range []string{" MLabels,", " MLabels\n", " Names =", " Events ="} {
		if strings.Contains(out, bad) {
			t.Errorf("generated C# has a member named like its type: %q", bad)
		}
	}
}

// Round 2, finding 7: a bound outside Int32 makes the numbers long; a normal contract stays int.
func TestWideBoundsUseLong(t *testing.T) {
	wide := render(t, `
service: s
types: { shard: { kind: int, min: -2147483649, max: 2147483648 } }
metrics: { m: { kind: counter, budget: 5000000000, labels: { shard: shard } } }
`)
	for _, want := range []string{"static abstract long Min", "public static long Max => 2147483648;", "private readonly long _value;", "DurianSignalResult<DurianSignalInt<TSpec>> Of(long n)", "    long Budget,"} {
		if !strings.Contains(wide, want) {
			t.Errorf("wide contract: generated C# lacks %q", want)
		}
	}
	narrow := render(t, "service: s\ntypes: { shard: { kind: int, min: 0, max: 15 } }\n")
	if strings.Contains(narrow, "long") || !strings.Contains(narrow, "public static int Max => 15;") {
		t.Error("an Int32 contract must keep int")
	}
}
