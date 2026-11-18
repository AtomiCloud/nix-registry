package profile

import (
	"strings"
	"testing"
)

func TestReviewHoveringMember(t *testing.T) {
	b := duplicateBaseline(t, duplicateSummary(declared(10, .014), declared(20, .2)))
	rows, extra := Compare(b, duplicateSummary(declared(10, .016), declared(20, .3)), nil)
	var out strings.Builder
	if PrintCompare(&out, "hover.json", rows, extra) != 1 || !strings.Contains(out.String(), "1 in baseline, 2 now") {
		t.Fatal(out.String())
	}
}
func TestReviewColdSplitAcrossBaselineRuns(t *testing.T) {
	a := duplicateSummary(declared(10, .02))
	z := duplicateSummary()
	c := duplicateSummary(declared(20, .02))
	b, err := BuildBaseline("s", "n", "", []string{"render"}, []*Summary{a, c, z})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Functions) != 0 || len(b.FunctionGroups) != 1 || b.FunctionGroups[0].Count != 1 {
		t.Fatalf("lost cold group: %+v", b)
	}
	rows, extra := Compare(b, duplicateSummary(declared(30, .02), declared(40, .1)), nil)
	var out strings.Builder
	if PrintCompare(&out, "split.json", rows, extra) != 1 || !strings.Contains(out.String(), "1 in baseline, 2 now") {
		t.Fatal(out.String())
	}
}
func TestReviewCompensatingThresholdCrossing(t *testing.T) {
	base := duplicateSummary(declared(10, .016), declared(20, .2), declared(30, .014))
	b := duplicateBaseline(t, base)
	rows, extra := Compare(b, duplicateSummary(declared(10, .014), declared(20, .3), declared(30, .016)), nil)
	var out strings.Builder
	n := PrintCompare(&out, "compensated.json", rows, extra)
	if n == 0 {
		t.Fatalf("unchanged locations must retain the hot row and catch +50%%:\n%s", out.String())
	}
}

func TestUniqueDeclarationMedianIgnoresLocationAndFirstRunCutoff(t *testing.T) {
	runs := []*Summary{
		duplicateSummary(declared(10, .01)),
		duplicateSummary(declared(20, .2)),
		duplicateSummary(declared(30, .2)),
	}
	b, err := BuildBaseline("s", "n", "", []string{"render"}, runs)
	if err != nil || len(b.Functions) != 1 || b.Functions[0].Share != .2 {
		t.Fatalf("unique moved declaration must retain 20%% median: baseline=%+v err=%v", b, err)
	}
	rows, _ := Compare(b, duplicateSummary(declared(40, .3)), nil)
	if len(rows) != 1 || !rows[0].Failed() || !near(rows[0].Change, .5, 1e-9) {
		t.Fatalf("unique moved declaration +50%% must fail: %+v", rows)
	}
}
