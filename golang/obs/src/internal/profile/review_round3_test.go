package profile

import (
	"strings"
	"testing"
)

func duplicateSummary(rows ...FuncRow) *Summary {
	return &Summary{Format: FormatCPUProfile, Samples: 10000, Functions: rows}
}

func declared(line int64, share float64) FuncRow {
	return FuncRow{Name: "render", File: "a.js", Line: line, Own: true, OwnShare: share, OwnN: int64(share * 10000)}
}

func duplicateBaseline(t *testing.T, base *Summary) *Baseline {
	t.Helper()
	b, err := BuildBaseline("s", "n", "", []string{"render"}, []*Summary{base, base, base})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSourceOrderShiftCannotSwapDuplicateFunctions(t *testing.T) {
	base := duplicateSummary(declared(10, .2), declared(20, .3))
	b := duplicateBaseline(t, base)
	rows, _ := Compare(b, duplicateSummary(declared(30, .2), declared(40, .3)), nil)
	for _, r := range rows {
		if r.Failed() || r.Change != 0 {
			t.Errorf("pure source shift must preserve order: %+v", r)
		}
	}
	rows, extra := Compare(b, duplicateSummary(declared(30, .3), declared(40, .2)), nil)
	var out strings.Builder
	if PrintCompare(&out, "source-order.baseline.json", rows, extra) != 1 || !strings.Contains(out.String(), "+50.0%") {
		t.Fatalf("nearest-location swap hid regression:\n%s", out.String())
	}
}

func TestNewDuplicateAtOldLocationIsAmbiguous(t *testing.T) {
	b := duplicateBaseline(t, duplicateSummary(declared(10, .2)))
	rows, extra := Compare(b, duplicateSummary(declared(10, .2), declared(100, .3)), nil)
	var out strings.Builder
	if PrintCompare(&out, "new-declaration.baseline.json", rows, extra) == 0 {
		t.Fatalf("new declaration stole the baseline identity:\n%s", out.String())
	}
	for _, needle := range []string{"ambiguous duplicate render in a.js", "1 in baseline, 2 now", "--update"} {
		if !strings.Contains(out.String(), needle) {
			t.Errorf("missing %q:\n%s", needle, out.String())
		}
	}
	if len(extra) > 1 || strings.Contains(strings.Join(extra, " "), "has no row in the baseline") {
		t.Errorf("ambiguous hot row became a note: %v", extra)
	}
}

func TestRemovedDuplicateNamesBothCounts(t *testing.T) {
	b := duplicateBaseline(t, duplicateSummary(declared(10, .2), declared(20, .3)))
	rows, extra := Compare(b, duplicateSummary(declared(20, .3)), nil)
	var out strings.Builder
	if PrintCompare(&out, "removed-declaration.baseline.json", rows, extra) == 0 || !strings.Contains(out.String(), "2 in baseline, 1 now") {
		t.Fatalf("duplicate-count decrease must be explicit:\n%s", out.String())
	}
}

func TestColdDuplicateContextDoesNotAddCompareRows(t *testing.T) {
	base := duplicateSummary(declared(10, .02), declared(20, .2))
	b := duplicateBaseline(t, base)
	if len(b.Functions) != 1 {
		t.Fatalf("cold duplicate must not be a row: %+v", b.Functions)
	}
	if !strings.Contains(string(b.Marshal()), `"declarations": 2`) {
		t.Errorf("hot row must retain the full declaration count: %s", b.Marshal())
	}
	rows, _ := Compare(b, base, nil)
	for _, r := range rows {
		if r.Failed() || r.Missing {
			t.Errorf("unchanged cold duplicate is not ambiguous: %+v", r)
		}
	}
}
