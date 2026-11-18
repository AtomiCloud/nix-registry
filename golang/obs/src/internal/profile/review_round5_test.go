package profile

import (
	"strings"
	"testing"
)

func r5(t *testing.T, base, now *Summary) (int, string) {
	b := duplicateBaseline(t, base)
	rows, extra := Compare(b, now, nil)
	var out strings.Builder
	n := PrintCompare(&out, "b.json", rows, extra)
	return n, out.String()
}

// Edit inserts 20 lines above both duplicates; the spacing between them is also 20.
// A (hot) is +50 %; B's old location now holds A.
func TestR5ShiftEqualsSpacing(t *testing.T) {
	n, out := r5(t, duplicateSummary(declared(10, .2), declared(30, .02), FuncRow{Name: "other", File: "b.js", Line: 1, Own: true, OwnShare: .78, OwnN: 7800}),
		duplicateSummary(declared(30, .3), declared(50, .02), FuncRow{Name: "other", File: "b.js", Line: 1, Own: true, OwnShare: .68, OwnN: 6800}))
	t.Logf("%d\n%s", n, out)
	if n == 0 {
		t.Error("FALSE GREEN: hot +50 % hidden when the shift equals duplicate spacing")
	}
}

// Same, with a cold member that falls under 1.5 % (minified-style many duplicates).
func TestR5ShiftColdCollision(t *testing.T) {
	n, out := r5(t, duplicateSummary(declared(10, .2), declared(30, .01), declared(60, .02), FuncRow{Name: "other", File: "b.js", Line: 1, Own: true, OwnShare: .77, OwnN: 7700}),
		duplicateSummary(declared(30, .3), declared(50, .01), declared(80, .02), FuncRow{Name: "other", File: "b.js", Line: 1, Own: true, OwnShare: .67, OwnN: 6700}))
	t.Logf("%d\n%s", n, out)
	if n == 0 {
		t.Error("FALSE GREEN: cold member collision steals the hot row")
	}
}

func TestUniformShiftCollisionUsesSourceOrder(t *testing.T) {
	b := duplicateBaseline(t, duplicateSummary(declared(10, .2), declared(20, .3)))
	rows, _ := Compare(b, duplicateSummary(declared(20, .2), declared(30, .3)), nil)
	for _, r := range rows {
		if r.Failed() || r.Change != 0 {
			t.Fatalf("uniform +10 shift must be unchanged: %+v", r)
		}
	}
	rows, extra := Compare(b, duplicateSummary(declared(20, .3), declared(30, .2)), nil)
	var out strings.Builder
	if PrintCompare(&out, "shift-collision.json", rows, extra) != 1 || !strings.Contains(out.String(), "+50.0%") {
		t.Fatalf("uniform +10 shift must retain the first member's +50%%: %s", out.String())
	}
}
