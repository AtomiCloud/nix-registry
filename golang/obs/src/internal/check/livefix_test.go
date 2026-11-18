package check

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/AtomiCloud/obs/internal/engines/vl"
	"github.com/AtomiCloud/obs/internal/engines/vm"
	"github.com/AtomiCloud/obs/internal/files"
	"github.com/AtomiCloud/obs/internal/seed"
)

// fakeLogs answers every stats query with rows only when the query holds none of the banned tokens,
// and keeps the query it was asked, so a test sees what --live really ran.
type fakeLogs struct {
	banned []string
	asked  []string
}

func (f *fakeLogs) Stats(qs string, _, _ time.Time, _ bool) ([]vl.Result, error) {
	f.asked = append(f.asked, qs)
	for _, b := range f.banned {
		if strings.Contains(qs, b) {
			return []vl.Result{{Value: "0"}}, nil // `stats count()` with no match: one row, 0
		}
	}
	return []vl.Result{{Value: "3"}}, nil
}

func (f *fakeLogs) Rows(string, time.Time, time.Time) ([]vl.Result, error) { return nil, nil }

type fakeMetrics struct{ asked []string }

func (f *fakeMetrics) Instant(q string, _ time.Time) ([]vm.Series, error) {
	f.asked = append(f.asked, q)
	return nil, nil // the seed holds no such series
}

func (f *fakeMetrics) Range(q string, _, _ time.Time, _ time.Duration) ([]vm.Series, error) {
	f.asked = append(f.asked, q)
	return nil, nil
}

func logAlert(expr string) files.Query {
	return files.Query{File: "alerts/gold/warning.yaml", Where: "rule gold query A", Kind: files.Logs, IsAlert: true, Expr: expr, RangeSecs: 300}
}

const goldFilter = `user_tier:=gold`

// An alert log filter on a free-form field is "assumed true" by --static and by `obs alerts`
// (concept 11.1); --live must drop it the same way, print the same note, and not fail no-rows
// because the seed does not hold the field.
func TestLiveDropsAssumedFilters(t *testing.T) {
	o := Options{Dir: "../../../tests/fixtures/good/observability"}
	q := logAlert(`_time:5m {durian.cloud.service="checkout"} "durian.cloud/event":="checkout.charge.failed" ` + goldFilter + ` | stats count() n`)
	fl := &fakeLogs{banned: []string{goldFilter}}
	r := runLive(&files.Set{Alerts: 1, Queries: []files.Query{q}}, o, &seed.Dataset{End: time.Unix(1800000000, 0)}, nil, fl)
	text := fmt.Sprint(r.Findings)
	if r.Failures() != 0 {
		t.Fatalf("an assumed filter must not fail the alert live: %s", text)
	}
	if !hasRule(r, "assumed-filter") || !strings.Contains(text, "assumed true (free-form fields): "+goldFilter) {
		t.Fatalf("want the assumed-filter note as obs alerts prints it, got %s", text)
	}
	if len(fl.asked) != 1 || strings.Contains(fl.asked[0], goldFilter) || !strings.Contains(fl.asked[0], "checkout.charge.failed") {
		t.Fatalf("live must run the query without the assumed filter and with the event filter, ran %v", fl.asked)
	}
	// Planted failure: with no contract the event rule is off and nothing is assumed, so the filter stays and the
	// query returns no rows: the red case proves the green one above is caused by the dropped filter.
	r = runLive(&files.Set{Alerts: 1, Queries: []files.Query{q}}, Options{Dir: t.TempDir()}, &seed.Dataset{End: time.Unix(1800000000, 0)}, nil, &fakeLogs{banned: []string{goldFilter}})
	if r.Failures() != 1 || !hasRule(r, "no-rows") {
		t.Fatalf("without a contract the free-form filter is kept and must fail no-rows, got %s", fmt.Sprint(r.Findings))
	}
}

func recordingSet(alert files.Query) *files.Set {
	rule := files.Rule{File: "alerts/record-rule/critical.yaml", Name: "fixture-record-rule"}
	rule.Definition.Record = &struct {
		Metric string `yaml:"metric"`
		From   string `yaml:"from"`
	}{Metric: "fixture_recorded", From: "A"}
	return &files.Set{Alerts: 2, Queries: []files.Query{alert}, Rules: []files.Rule{rule}}
}

// An alert query over a recording rule's output cannot run on the seed (the output exists only after
// `obs alerts` evaluates the rule). --live prints a named note and does not fail for that query; a
// metric that is neither in the seed nor recorded still fails no-rows.
func TestLiveNamesRecordingRuleReads(t *testing.T) {
	mq := func(expr string) files.Query {
		return files.Query{File: "alerts/recorded/critical.yaml", Where: "rule recorded query A", Kind: files.Metrics, IsAlert: true, Expr: expr, RangeSecs: 60}
	}
	ds := &seed.Dataset{End: time.Unix(1800000000, 0)}

	fm := &fakeMetrics{}
	r := runLive(recordingSet(mq(`fixture_recorded`)), Options{}, ds, fm, nil)
	text := fmt.Sprint(r.Findings)
	if r.Failures() != 0 {
		t.Fatalf("a query over a recording rule must not fail live: %s", text)
	}
	if !hasRule(r, "recording-rule") || !strings.Contains(text, "reads recording rule fixture_recorded: checked by obs alerts") ||
		!strings.Contains(text, "alerts/recorded/critical.yaml") || !strings.Contains(text, "rule recorded query A") {
		t.Fatalf("want the named note with file and rule, got %s", text)
	}
	if len(fm.asked) != 0 {
		t.Fatalf("the query over recorded output must not be run on the seed, ran %v", fm.asked)
	}

	r = runLive(recordingSet(mq(`fixture_typo`)), Options{}, ds, &fakeMetrics{}, nil)
	if r.Failures() != 1 || !hasRule(r, "no-rows") || hasRule(r, "recording-rule") {
		t.Fatalf("a metric that is not recorded must still fail no-rows, got %s", fmt.Sprint(r.Findings))
	}
}
