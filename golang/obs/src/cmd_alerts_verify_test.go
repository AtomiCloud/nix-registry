package main

import (
	"strings"
	"testing"
	"time"
)

var verifyBase = time.Date(2026, 11, 17, 8, 0, 0, 0, time.UTC)

// stepsJSON builds a two-minute, two-instance --steps document; the placeholders
// are what each test changes.
func stepsJSON(v1, v2, state2 string, withB bool) string {
	b := ""
	if withB {
		b = `,"{b}":"Normal"`
	}
	bv := ""
	if withB {
		bv = `,"{b}":0`
	}
	bs := ""
	if withB {
		bs = `,"{b}":"Normal"`
	}
	return `[{"alert":"logs","run":"spike","fired":true,"firing_steps":1,"steps":[` +
		`{"t":1794902460000,"e":{"{a}":"Normal"` + b + `},"v":{"{a}":` + v1 + bv + `},"s":{"{a}":"Normal"` + bs + `}},` +
		`{"t":1794902520000,"e":{"{a}":"Alerting"},"v":{"{a}":` + v2 + `},"s":{"{a}":"` + state2 + `"}}]}]`
}

func compareSteps(t *testing.T, pruned, full string) []pruneDiff {
	t.Helper()
	np, err := normalizeSteps([]byte(pruned), verifyBase)
	if err != nil {
		t.Fatal(err)
	}
	nf, err := normalizeSteps([]byte(full), verifyBase)
	if err != nil {
		t.Fatal(err)
	}
	var diffs []pruneDiff
	var st cmpStats
	diffTree(nil, np, nf, &st, &diffs)
	if st.Compared == 0 {
		t.Fatal("nothing compared")
	}
	return diffs
}

func wantOne(t *testing.T, diffs []pruneDiff, tokens ...string) {
	t.Helper()
	if len(diffs) != 1 {
		t.Fatalf("want exactly one difference, got %d: %v", len(diffs), diffs)
	}
	s := diffs[0].String()
	for _, tok := range tokens {
		if !strings.Contains(s, tok) {
			t.Fatalf("difference %q lacks %q", s, tok)
		}
	}
}

func TestPruneCompareIdenticalIsGreen(t *testing.T) {
	doc := stepsJSON("117", "30", "Alerting", true)
	if d := compareSteps(t, doc, doc); len(d) != 0 {
		t.Fatalf("identical input differs: %v", d)
	}
	// Formatting of an equal number is not a difference.
	if d := compareSteps(t, stepsJSON("1", "30", "Alerting", false), stepsJSON("1.0", "3e1", "Alerting", false)); len(d) != 0 {
		t.Fatalf("1 vs 1.0 and 30 vs 3e1 are equal values: %v", d)
	}
}

func TestPruneCompareChangedValueIsRed(t *testing.T) {
	// The union bug: 117 pruned, 234 full.
	d := compareSteps(t, stepsJSON("117", "30", "Alerting", false), stepsJSON("234", "30", "Alerting", false))
	wantOne(t, d, "alert logs", "run spike", "minute 2026-11-17T", "(+", "v > {a}", "pruned=117", "full=234")
}

func TestPruneCompareMissingInstanceIsRed(t *testing.T) {
	d := compareSteps(t, stepsJSON("1", "30", "Alerting", false), stepsJSON("1", "30", "Alerting", true))
	if len(d) != 3 { // e, v, s each lack {b} on the pruned side
		t.Fatalf("want 3 differences (e, v, s), got %d: %v", len(d), d)
	}
	for _, x := range d {
		if x.Pruned != missing || !strings.Contains(x.String(), "{b}") {
			t.Fatalf("expected pruned side missing for {b}: %v", x)
		}
	}
	// And the other way round: present only in the pruned run.
	d = compareSteps(t, stepsJSON("1", "30", "Alerting", true), stepsJSON("1", "30", "Alerting", false))
	if len(d) != 3 || d[0].Full != missing {
		t.Fatalf("expected full side missing: %v", d)
	}
}

func TestPruneCompareStateFlipIsRed(t *testing.T) {
	d := compareSteps(t, stepsJSON("1", "30", "Alerting", false), stepsJSON("1", "30", "Pending", false))
	wantOne(t, d, "alert logs", "run spike", "s > {a}", `pruned="Alerting"`, `full="Pending"`)
}

func TestPruneCompareNaNAndInf(t *testing.T) {
	nan := `"NaN"`
	if d := compareSteps(t, stepsJSON(nan, `"+Inf"`, "Alerting", false), stepsJSON(nan, `"+Inf"`, "Alerting", false)); len(d) != 0 {
		t.Fatalf("NaN = NaN and +Inf = +Inf must be equal: %v", d)
	}
	wantOne(t, compareSteps(t, stepsJSON(nan, "30", "Alerting", false), stepsJSON("0", "30", "Alerting", false)), `pruned="NaN"`, "full=0")
	wantOne(t, compareSteps(t, stepsJSON("5", "30", "Alerting", false), stepsJSON(nan, "30", "Alerting", false)), "pruned=5", `full="NaN"`)
	wantOne(t, compareSteps(t, stepsJSON(`"+Inf"`, "30", "Alerting", false), stepsJSON(`"-Inf"`, "30", "Alerting", false)), `"+Inf"`, `"-Inf"`)
	// Exactness: the last digit of a float counts, and -0 is not 0.
	wantOne(t, compareSteps(t, stepsJSON("0.30000000000000004", "30", "Alerting", false), stepsJSON("0.3", "30", "Alerting", false)), "0.30000000000000004")
	wantOne(t, compareSteps(t, stepsJSON("-0", "30", "Alerting", false), stepsJSON("0", "30", "Alerting", false)), "pruned=-0")
}

func TestPruneCompareSnapshotAndRecorded(t *testing.T) {
	a, _ := decodeJSON([]byte(`{"fixture-logs|hits|run spike":{"series":[{"labels":{"x":"1"},"points":[[1,2],[2,"NaN"]]}]}}`))
	same, _ := decodeJSON([]byte(`{"fixture-logs|hits|run spike":{"series":[{"labels":{"x":"1"},"points":[[1,2],[2,"NaN"]]}]}}`))
	var d []pruneDiff
	var st cmpStats
	diffTree([]string{"query input"}, a, same, &st, &d)
	if len(d) != 0 || st.Compared == 0 {
		t.Fatalf("equal snapshots: %v compared=%d", d, st.Compared)
	}
	changed, _ := decodeJSON([]byte(`{"fixture-logs|hits|run spike":{"series":[{"labels":{"x":"1"},"points":[[1,2],[2,7]]}]}}`))
	diffTree([]string{"query input"}, a, changed, &st, &d)
	wantOne(t, d, "query input > fixture-logs|hits|run spike", "points > [1] > [1]", `pruned="NaN"`, "full=7")

	r1, err := normalizeRecorded([]byte(`[{"labels":{"__name__":"rec"},"timestamps":[1],"values":[3]}]`))
	if err != nil {
		t.Fatal(err)
	}
	r2, _ := normalizeRecorded([]byte(`[]`))
	d, st = nil, cmpStats{}
	diffTree(nil, r1, r2, &st, &d)
	wantOne(t, d, "series", "__name__", "from t=1", "full="+missing)
}

func TestPruneBaseFromOut(t *testing.T) {
	if ms, ok := baseFromOut([]byte(`{"failed":0,"base_ms":1794038400000}`)); !ok || ms != 1794038400000 {
		t.Fatalf("base_ms: %d %v", ms, ok)
	}
	if _, ok := baseFromOut([]byte(`{"failed":0}`)); ok {
		t.Fatal("absent base_ms must report !ok")
	}
}

func TestPruneChildEnvDropsSwitches(t *testing.T) {
	env := childEnv([]string{"A=1", "OBS_NO_PRUNE=1", "OBS_CACHE_DIR=/x", "OBS_VERIFY_PRUNE=1", snapshotEnv + "=/y", plantSeedEnv + "=/z", "OBS_VERIFY_FAST=1"}, "OBS_NO_PRUNE=1")
	got := strings.Join(env, " ")
	if strings.Contains(got, "OBS_CACHE_DIR") || strings.Contains(got, "OBS_VERIFY_PRUNE=") || strings.Contains(got, "/y") || strings.Contains(got, "/z") {
		t.Fatalf("switches survive: %s", got)
	}
	if !strings.Contains(got, "A=1") || !strings.Contains(got, "OBS_VERIFY_FAST=1") || strings.Count(got, "OBS_NO_PRUNE=1") != 1 {
		t.Fatalf("unrelated env lost or extra not added once: %s", got)
	}
}
