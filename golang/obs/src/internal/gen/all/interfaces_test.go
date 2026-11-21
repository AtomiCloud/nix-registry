package all_test

import (
	"strings"
	"testing"

	"github.com/AtomiCloud/obs/internal/contract"
	"github.com/AtomiCloud/obs/internal/gen"
)

// This supplements the compiler fixtures: imports must come from the configured
// dependency, and generation must not quietly restore its own port/base types.
func TestGeneratedSignalsUseInterfaces(t *testing.T) {
	c, err := contract.Parse("contract.yaml", []byte(`
service: s
types:
  shard: {kind: int, min: 0, max: 9999}
metrics: {m: {kind: counter, budget: 10000, labels: {shard: shard}}}
logs: {Failed: {event: s.failed, level: error}}
`))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		lang, dependency, want, bad string
	}{
		{"go", "example.com/custom/obs", `obs "example.com/custom/obs"`, "func NewShard("},
		{"bun", "@example/interfaces", `from '@example/interfaces'`, "readonly __durianBounded:"},
		{"dotnet", "Example.Interfaces", "using Example.Interfaces;", "public readonly struct Int<"},
		{"dart", "custom_interfaces", "import 'package:custom_interfaces/diene_interfaces.dart';", "sealed class DurianSignalResult"},
	} {
		t.Run(tc.lang, func(t *testing.T) {
			files, err := gen.Generate(tc.lang, c, gen.Options{Interfaces: tc.dependency})
			if err != nil {
				t.Fatal(err)
			}
			var src string
			for _, f := range files {
				src += string(f.Content)
			}
			if !strings.Contains(src, tc.want) || strings.Contains(src, tc.bad) {
				t.Fatalf("want shared API %q and no %q:\n%s", tc.want, tc.bad, src)
			}
		})
	}
}

func TestPlanPassesTargetInterfaces(t *testing.T) {
	c, err := contract.Parse("contract.yaml", []byte(`
service: s
logs: {Failed: {event: s.failed, level: error}}
generate:
  - {lang: go, package_id: example.com/test, out: generated/go, interfaces: example.com/custom/obs}
  - {lang: bun, package_id: test, out: generated/bun, interfaces: '@example/interfaces'}
  - {lang: dotnet, package_id: test, out: generated/dotnet, interfaces: Example.Interfaces}
  - {lang: dart, package_id: test, out: generated/dart, interfaces: custom_interfaces}
`))
	if err != nil {
		t.Fatal(err)
	}
	outputs, err := gen.Plan(c, "observability", nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"go": "example.com/custom/obs", "bun": "@example/interfaces", "dotnet": "Example.Interfaces", "dart": "custom_interfaces"}
	for _, output := range outputs {
		if dependency, ok := want[output.Lang]; ok {
			if !strings.Contains(string(output.Content), dependency) {
				t.Errorf("%s did not use configured dependency %q", output.Path, dependency)
			}
			delete(want, output.Lang)
		}
	}
	if len(want) > 0 {
		t.Errorf("missing languages: %v", want)
	}
}
