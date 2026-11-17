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
