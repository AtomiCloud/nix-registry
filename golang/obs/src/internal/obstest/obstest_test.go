package obstest

import (
	"strings"
	"testing"

	"github.com/AtomiCloud/obs/internal/alerts"
	"github.com/AtomiCloud/obs/internal/seed"
)

func incident(name, start, dur string, fire, quiet []string) seed.Incident {
	var i seed.Incident
	i.Name = name
	i.Window.Start, i.Window.Duration = start, dur
	i.Expect.Fire, i.Expect.Quiet = fire, quiet
	return i
}

func TestIncidentCaughtByAPassingFireExpectation(t *testing.T) {
	incs := []seed.Incident{incident("spike", "10m", "10m", []string{"metric"}, nil)}
	vs := []alerts.Verdict{{Run: "spike", Alert: "metric", Expect: "fire", Pass: true}}
	if got := Uncaught(incs, vs, false); len(got) != 0 {
		t.Fatalf("caught incident reported: %+v", got)
	}
}

func TestIncidentWithOnlyQuietAlertsIsUncaught(t *testing.T) {
	incs := []seed.Incident{incident("spike", "10m", "10m", nil, []string{"metric"})}
	vs := []alerts.Verdict{{Run: "spike", Alert: "metric", Expect: "quiet", Pass: true}}
	got := Uncaught(incs, vs, false)
	if len(got) != 1 {
		t.Fatalf("want one finding, got %+v", got)
	}
	l := got[0].Line()
	for _, want := range []string{"FAIL incidents/spike.yaml", "incident spike", "window [10m0s,20m0s)", "[incident-uncaught]", "expect.fire is empty"} {
		if !strings.Contains(l, want) {
			t.Errorf("line %q lacks %q", l, want)
		}
	}
}

func TestFireExpectedButNotFiredIsUncaughtAndNamesTheAlert(t *testing.T) {
	incs := []seed.Incident{incident("spike", "10m", "10m", []string{"metric", "other"}, nil)}
	vs := []alerts.Verdict{
		{Run: "spike", Alert: "metric", Expect: "fire", Pass: false, Why: "quiet"},
		{Run: "spike", Alert: "other", Expect: "fire", Pass: false, Why: "fired before the incident window"},
	}
	got := Uncaught(incs, vs, false)
	if len(got) != 1 || !strings.Contains(got[0].Reason, "metric (quiet)") || !strings.Contains(got[0].Reason, "other (fired before the incident window)") {
		t.Fatalf("got %+v", got)
	}
}

func TestOnePassingAlertCatchesEvenIfAnotherMisses(t *testing.T) {
	incs := []seed.Incident{incident("spike", "10m", "10m", []string{"a", "b"}, nil)}
	vs := []alerts.Verdict{
		{Run: "spike", Alert: "a", Expect: "fire", Pass: false},
		{Run: "spike", Alert: "b", Expect: "fire", Pass: true},
	}
	if got := Uncaught(incs, vs, false); len(got) != 0 {
		t.Fatalf("caught incident reported: %+v", got)
	}
}

func TestAnotherIncidentsVerdictDoesNotCatch(t *testing.T) {
	incs := []seed.Incident{incident("one", "0m", "5m", []string{"a"}, nil), incident("two", "5m", "5m", nil, []string{"a"})}
	vs := []alerts.Verdict{{Run: "one", Alert: "a", Expect: "fire", Pass: true}, {Run: "two", Alert: "a", Expect: "quiet", Pass: true}}
	got := Uncaught(incs, vs, false)
	if len(got) != 1 || got[0].Incident != "two" {
		t.Fatalf("got %+v", got)
	}
}

func TestNoRulesMeansEveryIncidentIsUncaught(t *testing.T) {
	incs := []seed.Incident{incident("one", "0m", "5m", []string{"a"}, nil), incident("two", "5m", "5m", nil, []string{"a"})}
	got := Uncaught(incs, nil, true)
	if len(got) != 2 || !strings.Contains(got[0].Reason, "no alert rules") {
		t.Fatalf("got %+v", got)
	}
}

func TestNoIncidentsIsVacuouslyGreen(t *testing.T) {
	if got := Uncaught(nil, nil, false); len(got) != 0 {
		t.Fatalf("got %+v", got)
	}
}
