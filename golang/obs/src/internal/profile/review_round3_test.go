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
		if r.Failed() || r.Missing || r.Change != 0 {
			t.Errorf("unchanged cold duplicate is not ambiguous: %+v", r)
		}
	}
	rows, _ = Compare(b, duplicateSummary(declared(10, .02), declared(20, .3)), nil)
	if len(rows) != 1 || !rows[0].Failed() || !near(rows[0].Change, .5, 1e-9) {
		t.Fatalf("hot second declaration +50%% must fail: %+v", rows)
	}
}

func TestBaselineRejectsDifferentCountedDeclarationCounts(t *testing.T) {
	two := duplicateSummary(declared(10, .02), declared(20, .2))
	one := duplicateSummary(declared(20, .2))
	if _, err := BuildBaseline("s", "n", "", []string{"render"}, []*Summary{two, two, one}); err == nil || !strings.Contains(err.Error(), "2 in baseline, 1 now") {
		t.Fatalf("2,2,1 declarations must be rejected: %v", err)
	}
}

func TestSubthresholdDuplicateMayBeUnsampled(t *testing.T) {
	two := duplicateSummary(declared(10, .005), declared(20, .2))
	one := duplicateSummary(declared(20, .2))
	b, err := BuildBaseline("s", "n", "", []string{"render"}, []*Summary{two, two, one})
	if err != nil {
		t.Fatalf("0.5%% duplicate must not block baseline: %v", err)
	}
	for _, now := range []*Summary{two, one} {
		rows, _ := Compare(b, now, nil)
		for _, r := range rows {
			if r.Failed() || r.Change != 0 {
				t.Fatalf("0.5%% duplicate must not change the hot match: %+v", r)
			}
		}
	}
}

func TestNewHotMemberOfColdDuplicateGroupIsAmbiguous(t *testing.T) {
	base := duplicateSummary(declared(10, .01), declared(20, .02))
	b := duplicateBaseline(t, base)
	if len(b.Functions) != 0 {
		t.Fatalf("all baseline members are below 3%%: %+v", b.Functions)
	}
	rows, extra := Compare(b, duplicateSummary(declared(10, .01), declared(20, .02), declared(30, .1)), nil)
	var out strings.Builder
	if PrintCompare(&out, "cold-group.baseline.json", rows, extra) == 0 || !strings.Contains(out.String(), "1 in baseline, 2 now") {
		t.Fatalf("new hot duplicate must be an error, not a note:\n%s", out.String())
	}
}

func TestThresholdCrossingCannotMoveAnExactHotDeclaration(t *testing.T) {
	base := duplicateSummary(declared(10, .016), declared(20, .2), declared(30, .014))
	b := duplicateBaseline(t, base)
	for _, hot := range []float64{.2, .3} {
		now := duplicateSummary(declared(10, .014), declared(20, hot), declared(30, .016))
		rows, _ := Compare(b, now, nil)
		want := hot/.2 - 1
		if len(rows) != 1 || !near(rows[0].Change, want, 1e-9) || rows[0].Failed() != (hot == .3) {
			t.Errorf("fixed location, cold threshold crossing, hot=%g: %+v", hot, rows)
		}
	}
	// The formerly tiny third member must reserve its exact location before a
	// shifted hot member is paired with the remaining declaration.
	rows, _ := Compare(b, duplicateSummary(declared(10, .014), declared(40, .3), declared(30, .016)), nil)
	if len(rows) != 1 || !near(rows[0].Change, .5, 1e-9) || !rows[0].Failed() {
		t.Fatalf("cold exact matches must not steal a shifted hot member: %+v", rows)
	}
}

func TestBaselineThresholdCrossingKeepsExactDeclarationMedian(t *testing.T) {
	a := duplicateSummary(declared(10, .016), declared(20, .2), declared(30, .014))
	c := duplicateSummary(declared(10, .014), declared(20, .3), declared(30, .016))
	a.Functions[1].Column, c.Functions[1].Column = 9, 9
	b, err := BuildBaseline("s", "n", "", []string{"render"}, []*Summary{a, c, c})
	if err != nil || len(b.Functions) != 1 || b.Functions[0].Share != .3 {
		t.Fatalf("exact hot member median must be 30%%: baseline=%+v err=%v", b, err)
	}
	if f := b.Functions[0]; f.Line != 20 || f.Column != 9 || f.SourceOrder != 1 {
		t.Fatalf("30%% median must retain the hot declaration's identity: %+v", f)
	}
}
