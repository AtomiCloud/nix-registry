package profile

import "testing"

// obs.yaml thresholds.profiles move the default tolerances; a zero field keeps the built-in
// value and a per-service override still wins.
func TestLimitsDefaultsFromObsYaml(t *testing.T) {
	b := &Baseline{Service: "s", Scenario: "x"}
	var nilLimits *Limits
	if tol, _ := nilLimits.tolerance(b, "function", "f"); tol != TolFunction {
		t.Fatalf("nil limits: %v", tol)
	}
	l := &Limits{Defaults: Tolerances{Function: 0.10, Total: 0.20}}
	for _, tc := range []struct {
		kind string
		want float64
	}{{"function", 0.10}, {"total", 0.20}, {"span", TolSpan}} {
		if tol, _ := l.tolerance(b, tc.kind, "n"); tol != tc.want {
			t.Errorf("%s: tolerance %v, want %v", tc.kind, tol, tc.want)
		}
	}
	d := &Baseline{Service: "s", Scenario: "x", Profiler: ProfilerDotnetTrace}
	if tol, why := l.tolerance(d, "function", "f"); tol != TolDotnet || why == "" {
		t.Errorf("dotnet-trace keeps its built-in %v when obs.yaml does not set it, got %v (%q)", TolDotnet, tol, why)
	}
	l.Defaults.DotnetTrace = 0.5
	if tol, _ := l.tolerance(d, "function", "f"); tol != 0.5 {
		t.Errorf("dotnet-trace from obs.yaml: %v", tol)
	}
	l.Overrides = []Override{{Service: "s", Kind: "function", Limit: 0.9, Why: "w"}}
	if tol, _ := l.tolerance(b, "function", "f"); tol != 0.9 {
		t.Errorf("an override must beat the obs.yaml default: %v", tol)
	}
}
