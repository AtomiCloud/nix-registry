package live

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AtomiCloud/obs/internal/contract"
	"github.com/AtomiCloud/obs/internal/seed"
)

// liveFixtureDir is tests/fixtures/live-recorded: the repo the recorded sandbox was set up from.
var liveFixtureDir = filepath.Join("..", "..", "..", "tests", "fixtures", "live-recorded")

func liveFixtureContract(t *testing.T) *contract.Contract {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(liveFixtureDir, "contract.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := contract.Parse("contract.yaml", b)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestRecordedUsage(t *testing.T) {
	h := recordedStores(t, "usage")
	var e Estimates
	e.Logs.LinesPerMin = 60
	res, err := Usage(context.Background(), &Client{}, h.Target, liveFixtureContract(t), UsageOptions{Window: 5 * time.Minute, Pods: 1, Unknown: "warn", Now: func() time.Time { return h.Now }, Estimates: e})
	if err != nil {
		t.Fatal(err)
	}
	assertReadOnly(t, h.rec)
	// 2 series (outcome ok and error) in landscape "fixture", budget 4, worst case 2. The sandbox
	// seed writes a point every 15 s; the contract's 30 s interval allows 4 points/min for the
	// worst case (8 for the budget): the recorded 38 points in 5 minutes are 7.6/min, above the
	// worst case and inside the budget.
	if len(res.Metrics) != 1 {
		t.Fatalf("rows: %+v", res.Metrics)
	}
	m := res.Metrics[0]
	if m.Metric != "checkout.requests" || m.Series != 2 || m.Worst != 2 || m.Budget != 4 || len(m.Landscapes) != 1 || m.Landscapes[0].Landscape != "fixture" {
		t.Fatalf("%+v", m)
	}
	near(t, "points/min", m.DPM, 7.6, 1e-9)
	if m.WorstDPM != 4 || m.BudgetDPM != 8 || m.Status != StatusAboveWorst || !m.DPMOver {
		t.Fatalf("both thresholds and the status: %+v", m)
	}
	if res.OverBudget() || len(res.Findings) != 0 {
		t.Fatalf("nothing over budget, no label finding: %+v", res.Findings)
	}
	// logs: 299 lines and 4485 message bytes in 5 minutes, one stream, estimate 60 lines/min
	l := res.Logs
	near(t, "lines/min", l.LinesPerMin, 59.8, 1e-9)
	near(t, "bytes/min", l.BytesPerMin, 897, 1e-9)
	near(t, "lines ratio", l.LinesRatio, 59.8/60, 1e-9)
	if !l.Available || !l.Compared || l.Streams != 1 || l.StreamsOver {
		t.Fatalf("%+v", l)
	}
	// traces: Tempo's TraceQL metrics answered for the service: the mean of six one-minute rates x 60
	tr := res.Traces
	near(t, "spans/min", tr.SpansPerMin, (0.43333333333333335+2.5+2.5666666666666664+2.966666666666667+6.366666666666666+6.6)/6*60, 1e-9)
	if !tr.SpansAvailable || tr.SpansSource != "tempo metrics" || tr.BytesAvailable || tr.BytesPerMin != 0 {
		t.Fatalf("%+v", tr)
	}
}

func TestRecordedShape(t *testing.T) {
	h := recordedStores(t, "shape")
	sd, err := seed.Load(filepath.Join(liveFixtureDir, "seed.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	res, err := ShapeReport(context.Background(), &Client{}, h.Target, liveFixtureContract(t), ShapeOptions{Window: 7 * 24 * time.Hour, Step: 5 * time.Minute, Seed: sd, Now: func() time.Time { return h.Now }})
	if err != nil {
		t.Fatal(err)
	}
	assertReadOnly(t, h.rec)
	if len(res.Metrics) != 1 {
		t.Fatalf("%+v", res.Metrics)
	}
	m := res.Metrics[0]
	// The sandbox holds about two minutes of data in the last 7 days: one point at the 5 minute step.
	// Everything before it is a gap (2015 steps = 167h55m); the old code counted only gaps after the first sample.
	if m.Real == nil || m.Real.Points != 1 || m.Real.Level.Typical != 1.7875 {
		t.Fatalf("real: %+v %s", m.Real, m.RealNote)
	}
	if m.Real.Gaps.Count != 1 || m.Real.Gaps.Length != "167h55m" {
		t.Fatalf("the 7 day window with 5 minutes of data: %+v", m.Real.Gaps)
	}
	if m.Real.Trend.Known || m.Real.Cycle.DailyKnown {
		t.Fatalf("5 minutes of data know no trend or daily cycle: %+v", m.Real)
	}
	if m.Seed == nil || m.Seed.Points != 2 || m.Seed.Level.Typical < 1 || m.Seed.Level.Typical > 1.2 || res.SeedLength != "12m" {
		t.Fatalf("the seed side comes from the seed in the repo: %+v", m.Seed)
	}
}

func TestRecordedIncidents(t *testing.T) {
	h := recordedStores(t, "incidents")
	res, err := Incidents(context.Background(), &Client{}, h.Target, IncidentOptions{Dir: liveFixtureDir, Since: time.Hour, Now: func() time.Time { return h.Now }})
	if err != nil {
		t.Fatal(err)
	}
	assertReadOnly(t, h.rec)
	if !strings.Contains(res.Source, "Grafana state history") {
		t.Fatalf("source: %q", res.Source)
	}
	// Grafana's own state history: payment-errors went Normal -> Alerting at 20:16:30 UTC and was still
	// firing when the window ended 28 s later; payment-errors sustained is only Pending (for: 1m).
	if len(res.Fired) != 1 {
		t.Fatalf("fired: %+v", res.Fired)
	}
	f := res.Fired[0]
	if f.ID != "payment-errors" || f.Firings != 1 || f.Longest != 28*time.Second || !f.InRepo || f.Incident != "payment-spike" || f.StartedBeforeWindow {
		t.Fatalf("%+v", f)
	}
	if !f.First.Equal(time.Unix(1795032990, 0).UTC()) {
		t.Fatalf("first firing: %v", f.First)
	}
	if len(res.FiredNoIncident) != 0 || len(res.IncidentNotAlerted) != 0 {
		t.Fatalf("payment-spike expects payment-errors and it fired: %+v / %+v", res.FiredNoIncident, res.IncidentNotAlerted)
	}
	var noRun []string
	for _, a := range res.NoRunbook {
		noRun = append(noRun, a.ID)
	}
	if strings.Join(noRun, ",") != "payment-errors,payment-pending,payment-safety" {
		t.Fatalf("alert-without-runbook: %v", noRun)
	}
}

func TestRecordedLook(t *testing.T) {
	h := recordedStores(t, "look")
	res := Look(context.Background(), &Client{}, h.Target, LookOptions{Checklist: filepath.Join(liveFixtureDir, "checklist.md"), Out: "obs-look"})
	assertReadOnly(t, h.rec)
	if !res.OK || res.GrafanaVersion != "13.3.0" || len(res.Dashboards) != 1 || res.Dashboards[0].UID != "look-payments" || res.Dashboards[0].Title != "Seeded payments" {
		t.Fatalf("%+v", res)
	}
	if len(res.Checklist) != 8 || !strings.Contains(res.Prompt, "installs nothing") {
		t.Fatalf("%+v", res)
	}
}

func TestRecordedUsageAllServices(t *testing.T) {
	h := recordedStores(t, "usage-all")
	var e Estimates
	e.Logs.LinesPerMin = 60
	e.Traces.SpansPerMin = 100
	res, err := Usage(context.Background(), &Client{}, h.Target, liveFixtureContract(t), UsageOptions{Window: 5 * time.Minute, Pods: 1, Unknown: "warn", AllServices: true, Now: func() time.Time { return h.Now }, Estimates: e})
	if err != nil {
		t.Fatal(err)
	}
	assertReadOnly(t, h.rec)
	// the whole target: the stores answer for every service, nothing is compared with this service's estimates
	if len(res.Metrics) != 1 || res.Metrics[0].Series != 2 || res.Service != "" {
		t.Fatalf("%+v", res.Metrics)
	}
	if res.Logs.Compared || res.Logs.LinesRatio != 0 || res.Logs.StreamsOver {
		t.Fatalf("%+v", res.Logs)
	}
	tr := res.Traces
	near(t, "spans/min", tr.SpansPerMin, (2.5+2.5666666666666664+2.966666666666667+6.366666666666666+6.6+2.2333333333333334)/6*60, 1e-9)
	if !tr.SpansAvailable || tr.SpansRatio != 0 || tr.BytesAvailable || !strings.Contains(tr.BytesNote, "no tempo_distributor_bytes_received_total") {
		t.Fatalf("Tempo's own counters are not in the sandbox's VictoriaMetrics: %+v", tr)
	}
}
