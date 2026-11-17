package alerts

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/AtomiCloud/obs/internal/seed"
)

func TestNeededLogEvents(t *testing.T) {
	sets := logEventSets{
		"q-failed":   {"pay.failed"},
		"q-declined": {"pay.declined", "pay.failed"},
		"q-nocontr":  nil,
	}
	rule := func(exprs ...string) *Alert {
		a := &Alert{Queries: map[string]Query{"M": {Datasource: "metrics", Expr: "up"}}}
		for i, e := range exprs {
			a.Queries[fmt.Sprint(i)] = Query{Datasource: "logs", Expr: e}
		}
		return a
	}
	got := neededLogEvents([]*Alert{rule("q-failed"), rule("q-declined")}, sets)
	if want := map[string]bool{"pay.failed": true, "pay.declined": true}; !reflect.DeepEqual(got, want) {
		t.Fatalf("union: got %v want %v", got, want)
	}
	for name, rules := range map[string][]*Alert{
		"query without a contract (q-nocontr)": {rule("q-failed"), rule("q-nocontr")},
		"query never analysed (q-unknown)":     {rule("q-unknown")},
	} {
		if got := neededLogEvents(rules, sets); got != nil {
			t.Fatalf("%s: got %v, want nil (load every event)", name, got)
		}
	}
	t.Setenv("OBS_NO_PRUNE", "1")
	if got := neededLogEvents([]*Alert{rule("q-failed")}, sets); got != nil {
		t.Fatalf("OBS_NO_PRUNE=1: got %v, want nil", got)
	}
}

// Dropping the rows of events no rule reads must leave every other row, metric
// sample and timestamp of the seed unchanged (the lazy-load rule of §11.1.1).
func TestLazyLogsKeepOtherRowsIdentical(t *testing.T) {
	sd := seed.Seed{
		Length:     "20m",
		RandomSeed: 7,
		Resource:   map[string]string{"durian.cloud.service": "checkout"},
		Traffic:    seed.Traffic{Requests: "5/s"},
		Metrics: map[string]seed.MetricDef{"charges": {Type: "counter", Series: []seed.SeriesDef{
			{Where: map[string]string{"outcome": "ok"}, Share: 0.9},
			{Where: map[string]string{"outcome": "error"}, Share: 0.1},
		}}},
		Logs: []seed.LogDef{
			{Event: "pay.ok", From: seed.LogSource{Metric: "charges", Where: map[string]string{"outcome": "ok"}}},
			{Event: "pay.failed", Level: "error", From: seed.LogSource{Metric: "charges", Where: map[string]string{"outcome": "error"}}},
			{Event: "pay.any", From: seed.LogSource{Metric: "charges"}},
		},
	}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	full, err := seed.BuildIncident(&sd, start, nil)
	if err != nil {
		t.Fatal(err)
	}
	lazy := sd
	var dropped int
	lazy.Logs, dropped = keepLogs(sd.Logs, map[string]bool{"pay.failed": true})
	if dropped != 2 || len(lazy.Logs) != 1 || len(sd.Logs) != 3 {
		t.Fatalf("keepLogs: kept %d dropped %d, seed now has %d defs", len(lazy.Logs), dropped, len(sd.Logs))
	}
	part, err := seed.BuildIncident(&lazy, start, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(full.Series, part.Series) {
		t.Fatal("dropping log events changed metric samples")
	}
	pick := func(ds *seed.Dataset, event string) []seed.Row {
		var out []seed.Row
		for _, st := range ds.Streams {
			for _, r := range st.Rows {
				for _, f := range r.Fields {
					if f.Name == "durian.cloud/event" && f.Value == event {
						out = append(out, r)
					}
				}
			}
		}
		return out
	}
	want, got := pick(full, "pay.failed"), pick(part, "pay.failed")
	if len(want) == 0 {
		t.Fatal("fixture seed made no pay.failed rows")
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("pay.failed rows differ: full %d rows, lazy %d rows", len(want), len(got))
	}
	if n := len(pick(part, "pay.ok")) + len(pick(part, "pay.any")); n != 0 {
		t.Fatalf("lazy seed still has %d rows of dropped events", n)
	}
}
