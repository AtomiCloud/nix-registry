package dart

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

func TestGolden(t *testing.T) { gentest.Golden(t, "dart") }

// gen/stress is a second consumer repo: long names, reserved words, odd enum values.
func TestGoldenStress(t *testing.T) {
	fixtures := filepath.Dir(filepath.Dir(filepath.Dir(gentest.Fixture(t)))) // tests/fixtures/gen
	cpath := filepath.Join(fixtures, "stress", "observability", "contract.yaml")
	c, err := contract.LoadValid(cpath)
	if err != nil {
		t.Fatal(err)
	}
	fs, err := gen.Generate("dart", c, gen.Options{Package: "pay_signals"})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range fs {
		p := filepath.Join(fixtures, "stress", "gen", "dart", "lib", "src", "signals", f.Path)
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
		"  default_('default'),",
		"  class_('class'),",
		"  v1xx('1xx'),",
		"  wire_('Wire'),",
		"  all('All'),",
		`  itS('it\'s'),`,
		`  aB('a\$b'),`,
		"  required this.default_,",
		"extension type const VeryLongBoundedNumberTypeNameForWrapping._(int value)",
		"final class PaymentsProviderSettlementBatchReconciliationDurationMetric",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("generated Dart lacks %q", want)
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

// Every file starts with the standard header and names the contract's hash; no line is over 80
// columns (dart format would wrap it).
func TestHeaderAndWidth(t *testing.T) {
	c := fixtureContract(t)
	fs, err := gen.Generate("dart", c, gen.Options{Package: "checkout_signals"})
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
		for i, l := range strings.Split(string(f.Content), "\n") {
			if len(l) > width && !strings.HasPrefix(l, "//") && !strings.HasPrefix(strings.TrimSpace(l), "///") {
				t.Errorf("%s:%d is %d columns: %s", f.Path, i+1, len(l), l)
			}
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
	golden, err := os.ReadFile(filepath.Join(filepath.Dir(filepath.Dir(cpath)), "gen", "dart", "lib", "src", "signals", "signals.g.dart"))
	if err != nil {
		t.Fatal(err)
	}
	planted := strings.Replace(string(b), "shard: { kind: int, min: 0, max: 15 }", "shard: { kind: int, min: 0, max: 20 }", 1)
	if planted == string(b) {
		t.Fatal("the fixture changed: update the planted edit")
	}
	c, err := contract.Parse(cpath, []byte(planted))
	if err != nil || len(c.Validate()) > 0 {
		t.Fatalf("planted contract invalid: %v %v", err, c.Validate())
	}
	fs, err := gen.Generate("dart", c, gen.Options{Package: "checkout_signals"})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(fs[0].Content, golden) {
		t.Fatal("a planted range change left the generated file unchanged")
	}
	if !strings.Contains(string(fs[0].Content), "assert(v >= 0 && v <= 20)") {
		t.Error("the planted range is not in the generated file")
	}
	if gen.HashOf(fs[0].Content) == gen.HashOf(golden) {
		t.Error("the contract hash in the header did not change")
	}
}

// Two contract names that make one Dart name fail, naming both.
func TestCollisionsNamed(t *testing.T) {
	cases := map[string]struct{ src, want string }{
		"enum values": {
			"service: s\ntypes:\n  kind: { kind: enum, values: [a-b, a_b] }\n",
			`types.kind: "a-b" and "a_b" both make the Dart member "aB"`,
		},
		"shadowing dart:core": {
			"service: s\ntypes:\n  list: { kind: enum, values: [x] }\n",
			`types.list makes the Dart name "List", which would shadow a dart:core type`,
		},
		"a base type": {
			"service: s\ntypes:\n  durian_signal_wire: { kind: enum, values: [x] }\n",
			`a generated base type and types.durian_signal_wire both make the Dart name "DurianSignalWire"`,
		},
		"metric and span constants": {
			"service: s\nmetrics:\n  m.span: { kind: counter }\nspans:\n  m: {}\n",
			`metrics.m.span and spans.m both make the Dart name "mSpan"`,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c, err := contract.Parse("contract.yaml", []byte(tc.src))
			if err != nil {
				t.Fatal(err)
			}
			_, err = gen.Generate("dart", c, gen.Options{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want an error containing %q, got %v", tc.want, err)
			}
		})
	}
}
