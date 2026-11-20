package live

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const alertCR = `apiVersion: grafana.integreatly.org/v1beta1
kind: GrafanaAlertRuleGroup
metadata:
  name: %s
spec:
  interval: 1m
  rules:
    - uid: %s
      title: %s
      condition: C
%s      data: []
`

func writeRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	put := func(rel, body string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// high-errors: has a runbook, is in an incident. slow-charges: no runbook, in no incident. quiet-rule: no runbook, never fired.
	put("alerts/checkout/critical.yaml", fmt.Sprintf(alertCR, "checkout-critical", "high-errors", "High error rate", "      annotations:\n        runbook_url: https://runbooks.example/high-errors\n"))
	put("alerts/checkout/warning.yaml", fmt.Sprintf(alertCR, "checkout-warning", "slow-charges", "Slow charges", ""))
	put("alerts/checkout/info.yaml", fmt.Sprintf(alertCR, "checkout-info", "quiet-rule", "Quiet rule", ""))
	put("incidents/spike.yaml", "story: errors spike\nwindow: { start: 10m, duration: 10m }\nexpect: { fire: [high-errors], quiet: [slow-charges] }\n")
	put("incidents/orphan.yaml", "story: nothing watches this\nwindow: { start: 30m, duration: 5m }\nexpect: { quiet: [high-errors] }\n")
	return dir
}

// grafanaFake answers /api/annotations with state history entries.
func grafanaFake(t *testing.T, rec *recorder, body string, status int) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		if r.URL.Path != "/api/annotations" {
			http.Error(w, "unexpected "+r.URL.Path, 500)
			return
		}
		if status != 0 && status != 200 {
			http.Error(w, "nope", status)
			return
		}
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

var t0 = time.Date(2026, 11, 10, 14, 0, 0, 0, time.UTC)

func ann(uid, name, prev, next string, at time.Time) string {
	return fmt.Sprintf(`{"id":1,"alertId":7,"alertName":%q,"alertUID":%q,"prevState":%q,"newState":%q,"time":%d,"timeEnd":%d,"text":"x","data":{"labels":{}}}`, name, uid, prev, next, at.UnixMilli(), at.UnixMilli())
}

func TestIncidentsListsFiredWithoutIncidentMissingRunbookAndOrphanIncident(t *testing.T) {
	dir := writeRepo(t)
	rec := &recorder{}
	hist := "[" + strings.Join([]string{
		ann("high-errors", "High error rate", "Normal", "Alerting", t0),
		ann("high-errors", "High error rate", "Alerting", "Normal", t0.Add(20*time.Minute)),
		ann("slow-charges", "Slow charges", "Normal", "Alerting", t0.Add(time.Hour)),
		ann("slow-charges", "Slow charges", "Alerting", "Normal", t0.Add(80*time.Minute)),
		ann("slow-charges", "Slow charges", "Normal", "Alerting", t0.Add(2*time.Hour)),
		ann("stranger", "Not in the repo", "Normal", "Alerting", t0.Add(3*time.Hour)),
	}, ",") + "]"
	srv := grafanaFake(t, rec, hist, 0)
	now := t0.Add(24 * time.Hour)
	res, err := Incidents(context.Background(), &Client{}, &Target{Name: "prod", Grafana: srv.URL}, IncidentOptions{
		Dir: dir, Since: 7 * 24 * time.Hour, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Source, "Grafana state history") || !strings.Contains(res.SourceWhy, "ALERTS") {
		t.Fatalf("it must say which source and why: %q / %q", res.Source, res.SourceWhy)
	}
	if len(res.Fired) != 3 {
		t.Fatalf("fired: %+v", res.Fired)
	}
	got := map[string]FiredAlert{}
	for _, f := range res.Fired {
		got[f.ID] = f
	}
	if got["slow-charges"].Firings != 2 || got["high-errors"].Firings != 1 {
		t.Fatalf("firings: %+v", got)
	}
	var noInc []string
	for _, f := range res.FiredNoIncident {
		noInc = append(noInc, f.ID)
	}
	if strings.Join(noInc, ",") != "slow-charges,stranger" {
		t.Fatalf("fired-but-no-incident: %v", noInc)
	}
	if res.FiredNoIncident[1].InRepo {
		t.Fatalf("an alert the repo does not have must say so: %+v", res.FiredNoIncident[1])
	}
	if len(res.IncidentNotAlerted) != 1 || res.IncidentNotAlerted[0].Incident != "orphan" {
		t.Fatalf("incident-not-alerted: %+v", res.IncidentNotAlerted)
	}
	var noRun []string
	for _, a := range res.NoRunbook {
		noRun = append(noRun, a.ID)
	}
	if strings.Join(noRun, ",") != "quiet-rule,slow-charges" {
		t.Fatalf("alert-without-runbook: %v", noRun)
	}
	// Reports contain observations only: nothing is written.
	var sb strings.Builder
	res.Print(&sb)
	out := sb.String()
	for _, want := range []string{"fired-but-no-incident", "incident-not-alerted", "alert-without-runbook", "slow-charges", "orphan", "writes nothing"} {
		if !strings.Contains(out, want) {
			t.Errorf("text lacks %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "incidents", "slow-charges.yaml")); err == nil {
		t.Fatal("obs live incidents must never write an incident file")
	}
	assertReadOnly(t, rec)
}

func TestIncidentsNamedWindowWithoutObservationIsUnknown(t *testing.T) {
	dir := writeRepo(t)
	srv := grafanaWithCurrent(t, "["+strings.Join([]string{
		ann("high-errors", "High error rate", "Normal", "Alerting", t0),
		ann("high-errors", "High error rate", "Alerting", "Normal", t0.Add(10*time.Minute)),
		// Establish the other rules' states too; no transition would leave them unknown.
		ann("slow-charges", "Slow charges", "Pending", "Normal", t0.Add(-2*time.Hour)),
		ann("quiet-rule", "Quiet rule", "Pending", "Normal", t0.Add(-2*time.Hour)),
	}, ",")+"]", currentRulesBody(false))
	w1 := Window{From: t0.Add(-time.Hour), To: t0.Add(time.Hour)}        // an alert fired inside
	w2 := Window{From: t0.Add(5 * time.Hour), To: t0.Add(6 * time.Hour)} // nothing fired
	res, err := Incidents(context.Background(), &Client{}, &Target{Name: "prod", Grafana: srv.URL}, IncidentOptions{
		Dir: dir, Since: 7 * 24 * time.Hour, Windows: []Window{w1, w2}, Now: func() time.Time { return t0.Add(24 * time.Hour) },
	})
	if err != nil {
		t.Fatal(err)
	}
	// The only repository gap is the orphan fixture; the unobserved window adds none, whatever its identifier.
	if len(res.IncidentNotAlerted) != 1 || res.IncidentNotAlerted[0].Incident != "orphan" {
		t.Fatalf("a quiet-looking Grafana window is unknown, never a miss; only the orphan fixture is a gap: %+v", res.IncidentNotAlerted)
	}
	if len(res.Windows) != 2 || res.Windows[0].Status != "caught" || res.Windows[1].Status != "not_seen" || res.Windows[1].Unknown == "" {
		t.Fatalf("window 1 is caught, window 2 is not_seen with a reason: %+v", res.Windows)
	}
	var sb strings.Builder
	res.Print(&sb)
	if !strings.Contains(sb.String(), "cannot prove a miss") {
		t.Fatalf("the unobserved window must explain its uncertainty:\n%s", sb.String())
	}
}

func TestIncidentsFallsBackToAlertsSeriesWithoutGrafana(t *testing.T) {
	dir := writeRepo(t)
	rec := &recorder{}
	var queries []string
	now := t0.Add(24 * time.Hour)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		queries = append(queries, r.URL.Query().Get("query"))
		var vs []string
		for i := 0; i < 10; i++ {
			vs = append(vs, fmt.Sprintf(`[%d,"1"]`, t0.Unix()+int64(i)*60))
		}
		fmt.Fprintf(w, `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"alertname":"High error rate","alertstate":"firing","grafana_rule_uid":"high-errors"},"values":[%s]}]}}`, strings.Join(vs, ","))
	}))
	defer srv.Close()
	res, err := Incidents(context.Background(), &Client{}, &Target{Name: "dev", Metrics: srv.URL}, IncidentOptions{Dir: dir, Since: 7 * 24 * time.Hour, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Source, "ALERTS series") || !strings.Contains(res.SourceWhy, "no grafana URL") {
		t.Fatalf("source: %q why: %q", res.Source, res.SourceWhy)
	}
	if len(res.Fired) != 1 || res.Fired[0].ID != "high-errors" || !res.Fired[0].SpanUnknown || len(res.Fired[0].Observations) != 0 || res.Fired[0].TimeSource != "query_evaluation" {
		t.Fatalf("fired: %+v", res.Fired)
	}
	if len(queries) == 0 || !strings.Contains(queries[0], `ALERTS{alertstate="firing"}`) {
		t.Fatalf("queries: %v", queries)
	}
	assertReadOnly(t, rec)
}

func TestIncidentsGrafanaErrorWithMetricsFallsBackAndSaysSo(t *testing.T) {
	dir := writeRepo(t)
	rec := &recorder{}
	g := grafanaFake(t, rec, "", 403)
	vm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		fmt.Fprint(w, `{"status":"success","data":{"resultType":"matrix","result":[]}}`)
	}))
	defer vm.Close()
	res, err := Incidents(context.Background(), &Client{}, &Target{Name: "dev", Grafana: g.URL, Metrics: vm.URL}, IncidentOptions{Dir: dir, Since: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Source, "ALERTS series") || !strings.Contains(res.SourceWhy, "403") {
		t.Fatalf("%q %q", res.Source, res.SourceWhy)
	}
}

func TestIncidentsNoSourceIsAnError(t *testing.T) {
	_, err := Incidents(context.Background(), &Client{}, &Target{Name: "x"}, IncidentOptions{Dir: writeRepo(t), Since: time.Hour})
	if err == nil || !strings.Contains(err.Error(), "grafana") {
		t.Fatalf("%v", err)
	}
}

func TestParseWindow(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	w, err := ParseWindow("2026-11-10T14:00:00Z..2026-11-10T15:30:00Z", now)
	if err != nil || !w.From.Equal(t0) || !w.To.Equal(t0.Add(90*time.Minute)) {
		t.Fatalf("%+v %v", w, err)
	}
	w, err = ParseWindow("-6h..-2h", now)
	if err != nil || !w.From.Equal(now.Add(-6*time.Hour)) || !w.To.Equal(now.Add(-2*time.Hour)) {
		t.Fatalf("%+v %v", w, err)
	}
	w, err = ParseWindow("2026-11-10..2026-11-11", now)
	if err != nil || w.To.Sub(w.From) != 24*time.Hour {
		t.Fatalf("%+v %v", w, err)
	}
	for _, bad := range []string{"2026-11-10", "x..y", "2026-11-11..2026-11-10"} {
		if _, err := ParseWindow(bad, now); err == nil || !strings.Contains(err.Error(), bad) {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

type stateChange struct {
	uid, name, prev, next string
	at                    time.Time
}

// grafanaHistory answers /api/annotations like Grafana does: only the entries between the from
// and to the request names (milliseconds), so a test sees what a window read really returns.
func grafanaHistory(t *testing.T, rec *recorder, all []stateChange) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		if r.URL.Path != "/api/annotations" {
			http.Error(w, "unexpected "+r.URL.Path, 500)
			return
		}
		from, _ := strconv.ParseInt(r.URL.Query().Get("from"), 10, 64)
		to, _ := strconv.ParseInt(r.URL.Query().Get("to"), 10, 64)
		var parts []string
		for _, c := range all {
			if ms := c.at.UnixMilli(); ms >= from && ms <= to {
				parts = append(parts, ann(c.uid, c.name, c.prev, c.next, c.at))
			}
		}
		w.Write([]byte("[" + strings.Join(parts, ",") + "]"))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestIncidentsAlertFiringBeforeTheWindowThatClearsInsideIsReported(t *testing.T) {
	// The window's first transition is Alerting -> Normal. Starting the state at "not firing"
	// reported zero overlapping alerts for an alert that was firing for the first 10 minutes.
	dir := writeRepo(t)
	rec := &recorder{}
	now := t0.Add(time.Hour)
	srv := grafanaHistory(t, rec, []stateChange{
		{"high-errors", "High error rate", "Normal", "Alerting", t0},                       // before the window
		{"high-errors", "High error rate", "Alerting", "Normal", t0.Add(30 * time.Minute)}, // inside it
	})
	res, err := Incidents(context.Background(), &Client{}, &Target{Name: "prod", Grafana: srv.URL}, IncidentOptions{Dir: dir, Since: 40 * time.Minute, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Fired) != 1 || res.Fired[0].ID != "high-errors" {
		t.Fatalf("an alert that cleared inside the window was firing in it: %+v", res.Fired)
	}
	f := res.Fired[0]
	if !f.StartedBeforeWindow || f.Firings != 1 || !f.First.Equal(now.Add(-40*time.Minute)) || f.Longest != 10*time.Minute {
		t.Fatalf("first span runs from the window start (t0+20m) to the clear (t0+30m): %+v", f)
	}
	var sb strings.Builder
	res.Print(&sb)
	if !strings.Contains(sb.String(), "already firing when the window began") {
		t.Fatalf("text:\n%s", sb.String())
	}
	assertReadOnly(t, rec)
}

func TestIncidentsNoTransitionInWindowDoesNotProveCurrentFiring(t *testing.T) {
	dir := writeRepo(t)
	rec := &recorder{}
	now := t0.Add(2 * time.Hour)
	srv := grafanaHistory(t, rec, []stateChange{{"slow-charges", "Slow charges", "Normal", "Alerting", t0}})
	res, err := Incidents(context.Background(), &Client{}, &Target{Name: "prod", Grafana: srv.URL}, IncidentOptions{Dir: dir, Since: time.Hour, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Fired) != 0 {
		t.Fatalf("a transition before the window cannot prove firing inside it: %+v", res.Fired)
	}
	// an alert that cleared before the window began is not in it
	srv2 := grafanaHistory(t, rec, []stateChange{
		{"slow-charges", "Slow charges", "Normal", "Alerting", t0.Add(-2 * time.Hour)},
		{"slow-charges", "Slow charges", "Alerting", "Normal", t0.Add(-time.Hour)},
	})
	res, err = Incidents(context.Background(), &Client{}, &Target{Name: "prod", Grafana: srv2.URL}, IncidentOptions{Dir: dir, Since: time.Hour, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Fired) != 0 {
		t.Fatalf("over before the window: %+v", res.Fired)
	}
}

func TestIncidentsFirstEntryNormalWithPreviousAlertingNeedsNoEarlierHistory(t *testing.T) {
	// Grafana's own retention may hold no entry before the window: prevState says it was firing.
	dir := writeRepo(t)
	rec := &recorder{}
	now := t0.Add(time.Hour)
	srv := grafanaHistory(t, rec, []stateChange{{"high-errors", "High error rate", "Alerting", "Normal", t0.Add(40 * time.Minute)}})
	res, err := Incidents(context.Background(), &Client{}, &Target{Name: "prod", Grafana: srv.URL}, IncidentOptions{Dir: dir, Since: 30 * time.Minute, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Fired) != 1 || res.Fired[0].Longest != 0 || !res.Fired[0].StartedBeforeWindow || res.Fired[0].Firings != 1 {
		t.Fatalf("%+v", res.Fired)
	}
}

func TestIncidentsNamedWindowSeesAnAlertThatClearsInsideIt(t *testing.T) {
	dir := writeRepo(t)
	rec := &recorder{}
	now := t0.Add(48 * time.Hour)
	srv := grafanaHistory(t, rec, []stateChange{
		{"high-errors", "High error rate", "Normal", "Alerting", t0},
		{"high-errors", "High error rate", "Alerting", "Normal", t0.Add(30 * time.Minute)},
	})
	w := Window{From: t0.Add(15 * time.Minute), To: t0.Add(45 * time.Minute)}
	res, err := Incidents(context.Background(), &Client{}, &Target{Name: "prod", Grafana: srv.URL}, IncidentOptions{Dir: dir, Since: time.Hour, Windows: []Window{w}, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Windows) != 1 || len(res.Windows[0].Fired) != 1 || res.Windows[0].Fired[0] != "high-errors" {
		t.Fatalf("the named window holds the end of that firing: %+v", res.Windows)
	}
	for _, m := range res.IncidentNotAlerted {
		if m.Incident == "" {
			t.Fatalf("an alert did fire in the window: %+v", m)
		}
	}
}

func TestIncidentsRecoveringContinuesTheFiringSpan(t *testing.T) {
	srv := grafanaFake(t, &recorder{}, "["+strings.Join([]string{
		ann("high-errors", "High error rate", "Normal", "Alerting", t0),
		ann("high-errors", "High error rate", "Alerting", "Recovering", t0.Add(10*time.Minute)),
		ann("high-errors", "High error rate", "Recovering", "Alerting", t0.Add(20*time.Minute)),
		ann("high-errors", "High error rate", "Alerting", "Normal", t0.Add(30*time.Minute)),
	}, ",")+"]", 0)
	fired, _, err := fromAnnotations(context.Background(), &Client{}, &Target{Grafana: srv.URL}, t0, t0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	f := fired["high-errors"]
	if f == nil || f.Firings != 1 || len(f.spans) != 1 || f.spans[0].To.Sub(f.spans[0].From) != 30*time.Minute {
		t.Fatalf("Alerting -> Recovering -> Alerting must stay one 30m span: %+v", f)
	}
}

func TestIncidentsFirstFiringPreviousStateStartsAtLookback(t *testing.T) {
	for _, prev := range []string{"Alerting", "Recovering"} {
		t.Run(prev, func(t *testing.T) {
			srv := grafanaFake(t, &recorder{}, "["+ann("high-errors", "High error rate", prev, "Alerting", t0.Add(20*time.Minute))+"]", 0)
			fired, _, err := fromAnnotations(context.Background(), &Client{}, &Target{Grafana: srv.URL}, t0, t0.Add(time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			f := fired["high-errors"]
			if f == nil || !f.StartedBeforeWindow || !f.First.Equal(t0) {
				t.Fatalf("first prevState %s establishes firing at the lookback start: %+v", prev, f)
			}
		})
	}
}

func TestIncidentsNoTransitionIsUnknownRatherThanMissed(t *testing.T) {
	srv := grafanaFake(t, &recorder{}, "["+ann("high-errors", "High error rate", "Pending", "Normal", t0)+"]", 0)
	w := Window{From: t0, To: t0.Add(time.Hour)}
	res, err := Incidents(context.Background(), &Client{}, &Target{Name: "prod", Grafana: srv.URL}, IncidentOptions{Dir: writeRepo(t), Since: time.Hour, Windows: []Window{w}, Now: func() time.Time { return w.To }})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range res.IncidentNotAlerted {
		if m.Incident == "" {
			t.Fatalf("no transitions cannot establish a miss: %+v", m)
		}
	}
	var out strings.Builder
	res.Print(&out)
	if !strings.Contains(out.String(), "quiet-rule: state over the window is unknown from history") {
		t.Fatalf("report must name the rule whose state history cannot establish: %s", out.String())
	}
	found := false
	for _, r := range res.UnknownState {
		found = found || r.ID == "quiet-rule"
	}
	if !found {
		t.Fatal("UnknownState must include quiet-rule even when another rule has transitions")
	}
}

func TestIncidentsCappedHistoryCannotEstablishAMiss(t *testing.T) {
	entries := make([]string, 10000)
	for i := range entries {
		entries[i] = ann("quiet-rule", "Quiet rule", "Normal", "Pending", t0.Add(2*time.Hour))
	}
	entries[0] = ann("high-errors", "High error rate", "Pending", "Normal", t0.Add(-time.Hour))
	entries[1] = ann("slow-charges", "Slow charges", "Pending", "Normal", t0.Add(-time.Hour))
	srv := grafanaFake(t, &recorder{}, "["+strings.Join(entries, ",")+"]", 0)
	w := Window{From: t0, To: t0.Add(time.Hour)}
	res, err := Incidents(context.Background(), &Client{}, &Target{Name: "prod", Grafana: srv.URL}, IncidentOptions{Dir: writeRepo(t), Since: 3 * time.Hour, Windows: []Window{w}, Now: func() time.Time { return t0.Add(3 * time.Hour) }})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range res.IncidentNotAlerted {
		if m.Incident == "" {
			t.Fatalf("capped history cannot establish a miss: %+v", m)
		}
	}
	var out strings.Builder
	res.Print(&out)
	if !strings.Contains(out.String(), "incomplete") || !strings.Contains(out.String(), "10000") {
		t.Fatalf("report must state that history reached the limit: %s", out.String())
	}
}

func TestIncidentsInstancesMergeOverlappingSpansPerRule(t *testing.T) {
	withTags := func(prev, next, instance string, at time.Time, reverse bool) string {
		tags := []string{"alertname:High error rate", "instance:" + instance}
		if reverse {
			tags[0], tags[1] = tags[1], tags[0]
		}
		return historyRow("high-errors", 7, "High error rate", prev, next, at, map[string]string{"instance": instance}, tags)
	}
	srv := grafanaFake(t, &recorder{}, "["+strings.Join([]string{
		withTags("Normal", "Alerting", "a", t0, false),
		withTags("Normal", "Alerting", "b", t0.Add(10*time.Minute), false),
		withTags("Alerting", "Normal", "a", t0.Add(20*time.Minute), true),
		withTags("Alerting", "Normal", "b", t0.Add(40*time.Minute), true),
		withTags("Normal", "Alerting", "a", t0.Add(50*time.Minute), false),
		withTags("Alerting", "Normal", "a", t0.Add(55*time.Minute), true),
	}, ",")+"]", 0)
	fired, _, err := fromAnnotations(context.Background(), &Client{}, &Target{Grafana: srv.URL}, t0, t0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	f := fired["high-errors"]
	if f == nil || f.Firings != 2 || len(f.spans) != 2 || f.spans[0].To.Sub(f.spans[0].From) != 40*time.Minute || !f.Last.Equal(t0.Add(50*time.Minute)) {
		t.Fatalf("A resolving cannot close B; overlapping instances merge into one span: %+v", f)
	}
}

// historyRow lets these tests distinguish the lossless optional label map from Grafana's
// real, lossy tags (colons replaced with underscores and long labels omitted).
func historyRow(uid string, id int, title, prev, next string, at time.Time, labels map[string]string, tags []string) string {
	row := map[string]any{"alertUID": uid, "alertId": id, "alertName": title, "prevState": prev, "newState": next, "time": at.UnixMilli(), "tags": tags}
	if labels != nil {
		row["data"] = map[string]any{"labels": labels}
	}
	b, _ := json.Marshal(row)
	return string(b)
}

func TestIncidentsLosslessLabelsSurviveTagCollisionsAndOmission(t *testing.T) {
	for _, tags := range [][]string{{"instance:api_9090"}, nil} {
		srv := grafanaFake(t, &recorder{}, "["+strings.Join([]string{
			historyRow("high-errors", 7, "High error rate", "Normal", "Alerting", t0, map[string]string{"instance": "api:9090"}, tags),
			historyRow("high-errors", 7, "High error rate", "Normal", "Alerting", t0.Add(10*time.Minute), map[string]string{"instance": "api_9090"}, tags),
			historyRow("high-errors", 7, "High error rate", "Alerting", "Normal", t0.Add(20*time.Minute), map[string]string{"instance": "api:9090"}, tags),
			historyRow("high-errors", 7, "High error rate", "Alerting", "Normal", t0.Add(40*time.Minute), map[string]string{"instance": "api_9090"}, tags),
		}, ",")+"]", 0)
		fired, _, err := fromAnnotations(context.Background(), &Client{}, &Target{Grafana: srv.URL}, t0, t0.Add(time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		f := fired["high-errors"]
		if f == nil || len(f.spans) != 1 || f.spans[0].To.Sub(f.spans[0].From) != 40*time.Minute {
			t.Fatalf("lossy tags cannot merge distinct label maps: %+v", f)
		}
	}
}

func TestIncidentsTagOnlyHistoryHasUnknownSpans(t *testing.T) {
	srv := grafanaFake(t, &recorder{}, "["+historyRow("high-errors", 7, "High error rate", "Normal", "Alerting", t0, nil, []string{"instance:api_9090"})+","+historyRow("high-errors", 7, "High error rate", "Alerting", "Normal", t0.Add(20*time.Minute), nil, []string{"instance:api_9090"})+"]", 0)
	res, err := Incidents(context.Background(), &Client{}, &Target{Grafana: srv.URL}, IncidentOptions{Dir: writeRepo(t), Since: time.Hour, Now: func() time.Time { return t0.Add(time.Hour) }})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Fired) != 1 || res.Fired[0].Longest != 0 || len(res.Fired[0].spans) != 0 {
		t.Fatalf("tags cannot establish an instance span: %+v", res.Fired)
	}
	b, _ := res.JSON()
	if !strings.Contains(string(b), `"span_unknown": true`) {
		t.Fatalf("JSON must flag ambiguous spans: %s", b)
	}
}

func TestIncidentsNumericRuleIDsWithTheSameTitleStaySeparate(t *testing.T) {
	rows := []string{}
	for _, id := range []int{41, 42} {
		rows = append(rows, historyRow("", id, "Shared title", "Normal", "Alerting", t0, map[string]string{}, nil), historyRow("", id, "Shared title", "Alerting", "Normal", t0.Add(time.Duration(id-20)*time.Minute), map[string]string{}, nil))
	}
	srv := grafanaFake(t, &recorder{}, "["+strings.Join(rows, ",")+"]", 0)
	res, err := Incidents(context.Background(), &Client{}, &Target{Grafana: srv.URL}, IncidentOptions{Dir: writeRepo(t), Since: time.Hour, Now: func() time.Time { return t0.Add(time.Hour) }})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Fired) != 2 || res.Fired[0].ID == res.Fired[1].ID {
		t.Fatalf("different Grafana alertId values must stay distinct even with equal titles: %+v", res.Fired)
	}
}

func TestIncidentsConflictingUIDNeverMatchesByTitle(t *testing.T) {
	srv := grafanaFake(t, &recorder{}, "["+historyRow("different-rule", 41, "High error rate", "Normal", "Alerting", t0, map[string]string{}, nil)+"]", 0)
	res, err := Incidents(context.Background(), &Client{}, &Target{Grafana: srv.URL}, IncidentOptions{Dir: writeRepo(t), Since: time.Hour, Now: func() time.Time { return t0.Add(time.Hour) }})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Fired) != 1 || res.Fired[0].InRepo || res.Fired[0].ID != "different-rule" {
		t.Fatalf("an explicit conflicting UID rules out title matching: %+v", res.Fired)
	}
	for _, r := range res.UnknownState {
		if r.ID == "high-errors" {
			return
		}
	}
	t.Fatal("a different rule's same title cannot establish high-errors' state")
}

func TestIncidentsRepoTitleMatchRequiresUniqueRule(t *testing.T) {
	dir := writeRepo(t)
	file := filepath.Join(dir, "alerts", "checkout", "warning.yaml")
	if err := os.WriteFile(file, []byte(fmt.Sprintf(alertCR, "checkout-warning", "slow-charges", "High error rate", "")), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := grafanaFake(t, &recorder{}, "["+historyRow("", 41, "High error rate", "Normal", "Alerting", t0, map[string]string{}, nil)+"]", 0)
	res, err := Incidents(context.Background(), &Client{}, &Target{Grafana: srv.URL}, IncidentOptions{Dir: dir, Since: time.Hour, Now: func() time.Time { return t0.Add(time.Hour) }})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Fired) != 1 || res.Fired[0].InRepo {
		t.Fatalf("same-title repo rules cannot be matched by title: %+v", res.Fired)
	}
}

func TestIncidentsSeenRulesCannotEstablishInstanceCoverage(t *testing.T) {
	rows := []string{}
	for id, title := range map[string]string{"high-errors": "High error rate", "slow-charges": "Slow charges", "quiet-rule": "Quiet rule"} {
		rows = append(rows, historyRow(id, 7, title, "Pending", "Normal", t0.Add(-time.Hour), map[string]string{"instance": "quiet"}, nil))
	}
	srv := grafanaFake(t, &recorder{}, "["+strings.Join(rows, ",")+"]", 0)
	w := Window{From: t0, To: t0.Add(time.Hour)}
	res, err := Incidents(context.Background(), &Client{}, &Target{Grafana: srv.URL}, IncidentOptions{Dir: writeRepo(t), Since: time.Hour, Windows: []Window{w}, Now: func() time.Time { return w.To }})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range res.IncidentNotAlerted {
		if m.Incident == "" {
			t.Fatalf("another instance can have fired before the lookback with no transition: %+v", m)
		}
	}
	if len(res.Windows) != 1 || res.Windows[0].Unknown == "" {
		t.Fatalf("all known rules having rows still cannot prove complete instance inventory: %+v", res.Windows)
	}
}

func currentRulesBody(active bool) string {
	rules := []map[string]any{}
	for id, title := range map[string]string{"high-errors": "High error rate", "slow-charges": "Slow charges", "quiet-rule": "Quiet rule"} {
		r := map[string]any{"uid": id, "name": title, "type": "alerting", "state": "inactive", "health": "ok", "alerts": []any{}, "lastEvaluation": t0.Add(30 * time.Minute).Format(time.RFC3339)}
		if active && id == "high-errors" {
			r["state"] = "firing"
			r["alerts"] = []any{map[string]any{"state": "Alerting", "labels": map[string]string{"instance": "silent"}, "activeAt": t0.Add(-8 * 24 * time.Hour).Format(time.RFC3339)}}
		}
		rules = append(rules, r)
	}
	b, _ := json.Marshal(map[string]any{"status": "success", "data": map[string]any{"groups": []any{map[string]any{"rules": rules}}}})
	return string(b)
}

func grafanaWithCurrent(t *testing.T, history, current string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/annotations":
			fmt.Fprint(w, history)
		case "/api/prometheus/grafana/api/v1/rules":
			t.Error("Grafana history reports must not infer historical misses from a current-state read")
			fmt.Fprint(w, current)
		default:
			http.Error(w, "unexpected", 500)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestIncidentsEmptyHistoryCannotEstablishAQuietWindow(t *testing.T) {
	srv := grafanaWithCurrent(t, "[]", currentRulesBody(false))
	w := Window{From: t0, To: t0.Add(time.Hour)}
	res, err := Incidents(context.Background(), &Client{}, &Target{Grafana: srv.URL}, IncidentOptions{Dir: writeRepo(t), Since: time.Hour, Windows: []Window{w}, Now: func() time.Time { return w.To }})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Windows) != 1 || !strings.Contains(res.Windows[0].Unknown, "no alert state history returned; cannot tell quiet from history not kept in annotations") {
		t.Fatalf("empty annotations cannot prove quiet history: %+v", res.Windows)
	}
	for _, m := range res.IncidentNotAlerted {
		if m.Incident == "" {
			t.Fatal("empty Grafana history cannot create a miss")
		}
	}
}

func TestIncidentsNumericIDReconcilesExplicitUIDAcrossTitles(t *testing.T) {
	history := "[" + historyRow("different-rule", 41, "Previous title", "Pending", "Normal", t0.Add(-time.Hour), map[string]string{}, nil) + "," + historyRow("", 41, "High error rate", "Normal", "Alerting", t0, map[string]string{}, nil) + "]"
	srv := grafanaFake(t, &recorder{}, history, 0)
	res, err := Incidents(context.Background(), &Client{}, &Target{Grafana: srv.URL}, IncidentOptions{Dir: writeRepo(t), Since: time.Hour, Now: func() time.Time { return t0.Add(time.Hour) }})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Fired) != 1 || res.Fired[0].ID != "different-rule" || res.Fired[0].GrafanaAlertID != 41 || res.Fired[0].IDSource != "uid_from_alert_id" {
		t.Fatalf("ID provenance must retain numeric identity and its explicit UID evidence: %+v", res.Fired)
	}
	for _, f := range res.Fired {
		if f.InRepo || f.ID == "high-errors" {
			t.Fatalf("numeric alertId has explicit conflicting UID evidence under its previous title: %+v", res.Fired)
		}
	}
}

func TestIncidentsTitleUniquenessIncludesEarlierTitlesOfOtherRules(t *testing.T) {
	history := "[" + strings.Join([]string{
		historyRow("different-rule", 41, "High error rate", "Pending", "Normal", t0.Add(-2*time.Hour), map[string]string{}, nil),
		historyRow("different-rule", 41, "Renamed external rule", "Pending", "Normal", t0.Add(-time.Hour), map[string]string{}, nil),
		historyRow("", 42, "High error rate", "Normal", "Alerting", t0, map[string]string{}, nil),
	}, ",") + "]"
	srv := grafanaFake(t, &recorder{}, history, 0)
	res, err := Incidents(context.Background(), &Client{}, &Target{Grafana: srv.URL}, IncidentOptions{Dir: writeRepo(t), Since: time.Hour, Now: func() time.Time { return t0.Add(time.Hour) }})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Fired) != 1 || res.Fired[0].InRepo {
		t.Fatalf("a prior title on another rule is still conflicting title evidence: %+v", res.Fired)
	}
}

func TestIncidentsWindowBetweenTransitionsHasNoDirectFiringEvidence(t *testing.T) {
	history := "[" + historyRow("high-errors", 41, "High error rate", "Normal", "Alerting", t0, map[string]string{}, nil) + "," + historyRow("high-errors", 41, "High error rate", "Alerting", "Normal", t0.Add(40*time.Minute), map[string]string{}, nil) + "]"
	srv := grafanaFake(t, &recorder{}, history, 0)
	w := Window{From: t0.Add(10 * time.Minute), To: t0.Add(20 * time.Minute)}
	res, err := Incidents(context.Background(), &Client{}, &Target{Grafana: srv.URL}, IncidentOptions{Dir: writeRepo(t), Since: time.Hour, Windows: []Window{w}, Now: func() time.Time { return t0.Add(time.Hour) }})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Windows) != 1 || res.Windows[0].Status != "not_seen" || len(res.Windows[0].Fired) != 0 {
		t.Fatalf("a reconstructed span is not direct evidence inside this window: %+v", res.Windows)
	}
}

func TestIncidentsConflictingUIDsForOneNumericIDBlockTitleFallback(t *testing.T) {
	history := "[" + strings.Join([]string{
		historyRow("external-a", 41, "First external title", "Pending", "Normal", t0.Add(-2*time.Hour), map[string]string{}, nil),
		historyRow("external-b", 41, "Second external title", "Pending", "Normal", t0.Add(-time.Hour), map[string]string{}, nil),
		historyRow("", 41, "High error rate", "Normal", "Alerting", t0, map[string]string{}, nil),
	}, ",") + "]"
	srv := grafanaFake(t, &recorder{}, history, 0)
	res, err := Incidents(context.Background(), &Client{}, &Target{Grafana: srv.URL}, IncidentOptions{Dir: writeRepo(t), Since: time.Hour, Now: func() time.Time { return t0.Add(time.Hour) }})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Fired) != 1 || res.Fired[0].InRepo || res.Fired[0].IDSource != "ambiguous_alert_id" {
		t.Fatalf("two explicit UIDs for one numeric ID must forbid the otherwise unique repo title: %+v", res.Fired)
	}
}

func TestIncidentsEmptyAlertsFallbackCannotProveAMiss(t *testing.T) {
	g := grafanaFake(t, &recorder{}, "", http.StatusForbidden)
	vm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"status":"success","data":{"resultType":"matrix","result":[]}}`)
	}))
	defer vm.Close()
	w := Window{From: t0, To: t0.Add(time.Hour)}
	res, err := Incidents(context.Background(), &Client{}, &Target{Grafana: g.URL, Metrics: vm.URL}, IncidentOptions{Dir: writeRepo(t), Since: time.Hour, Windows: []Window{w}, Now: func() time.Time { return w.To }})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Windows) != 1 || res.Windows[0].Status != "not_seen" || !strings.Contains(res.Windows[0].Unknown, "Grafana history unavailable") || !strings.Contains(res.Windows[0].Unknown, "ALERTS cannot see Grafana-managed rules") {
		t.Fatalf("an empty ruler series cannot establish a Grafana miss: %+v", res.Windows)
	}
	for _, m := range res.IncidentNotAlerted {
		if m.Incident == "" {
			t.Fatalf("fallback cannot emit a named-window miss: %+v", m)
		}
	}
}

func TestIncidentsNoSuggestedEntriesForObservedOrUnobservedFiring(t *testing.T) {
	srv := grafanaFake(t, &recorder{}, "["+ann("slow-charges", "Slow charges", "Normal", "Alerting", t0)+"]", 0)
	res, err := Incidents(context.Background(), &Client{}, &Target{Grafana: srv.URL}, IncidentOptions{Dir: writeRepo(t), Since: time.Hour, Now: func() time.Time { return t0.Add(time.Hour) }})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := res.JSON()
	var out strings.Builder
	res.Print(&out)
	if strings.Contains(string(b), "suggested_incident_entries") || strings.Contains(string(b), "suggested_entry") || strings.Contains(out.String(), "SUGGESTED incident.yaml") {
		t.Fatalf("observational reports do not generate suggested incident entries: %s\n%s", b, out.String())
	}
}

func TestIncidentsFutureWindowRendersOnlyPartialEvidence(t *testing.T) {
	srv := grafanaFake(t, &recorder{}, "["+ann("high-errors", "High error rate", "Normal", "Alerting", t0.Add(20*time.Minute))+"]", 0)
	w := Window{From: t0, To: t0.Add(2 * time.Hour)}
	res, err := Incidents(context.Background(), &Client{}, &Target{Grafana: srv.URL}, IncidentOptions{Dir: writeRepo(t), Since: time.Hour, Windows: []Window{w}, Now: func() time.Time { return t0.Add(time.Hour) }})
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	res.Print(&out)
	if strings.Contains(out.String(), "caught:") || !strings.Contains(out.String(), "partial evidence") {
		t.Fatalf("not-yet-observable windows must not render as caught: %s", out.String())
	}
	b, _ := res.JSON()
	var body struct {
		Windows []struct {
			Status  string   `json:"status"`
			Fired   []string `json:"fired"`
			Partial []string `json:"partial_fired"`
		} `json:"windows"`
	}
	if err := json.Unmarshal(b, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Windows) != 1 || body.Windows[0].Status != "not_yet_observable" || len(body.Windows[0].Fired) != 0 || len(body.Windows[0].Partial) != 1 {
		t.Fatalf("JSON must label the firing as partial evidence too: %s", b)
	}
}

func TestIncidentsMissingTransitionMarksSpanUnknownWithoutZeroTimes(t *testing.T) {
	history := "[" + strings.Join([]string{
		historyRow("high-errors", 41, "High error rate", "Normal", "Alerting", t0.Add(-30*time.Minute), map[string]string{}, nil),
		historyRow("high-errors", 41, "High error rate", "Alerting", "Normal", t0.Add(10*time.Minute), map[string]string{}, nil),
		historyRow("high-errors", 41, "High error rate", "Alerting", "Normal", t0.Add(30*time.Minute), map[string]string{}, nil),
	}, ",") + "]"
	srv := grafanaFake(t, &recorder{}, history, 0)
	res, err := Incidents(context.Background(), &Client{}, &Target{Grafana: srv.URL}, IncidentOptions{Dir: writeRepo(t), Since: time.Hour, Now: func() time.Time { return t0.Add(time.Hour) }})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Fired) != 1 || !res.Fired[0].SpanUnknown || !res.Fired[0].First.Equal(t0.Add(10*time.Minute)) || !res.Fired[0].Last.Equal(t0.Add(30*time.Minute)) || res.Fired[0].StartedBeforeWindow {
		t.Fatalf("disagreement with the previous reconstructed state must preserve only direct evidence: %+v", res.Fired)
	}
	var out strings.Builder
	res.Print(&out)
	if strings.Contains(out.String(), "0001-") {
		t.Fatalf("never print a year-zero firing time: %s", out.String())
	}
}

func TestIncidentsAlertsFallbackUsesRawSampleTimesNotEvaluationTimes(t *testing.T) {
	for _, offset := range []time.Duration{50 * time.Second, 50*time.Second + 125*time.Millisecond, -58 * time.Minute} {
		t.Run(offset.String(), func(t *testing.T) {
			rawAt := t0.Add(offset)
			rec := &recorder{}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				rec.record(r)
				at := t0.Add(time.Minute) // query_range carries the older sample forward to this evaluation.
				if offset < 0 {
					at = rawAt
				} // left-boundary control: an evaluation can coincide with a raw sample.
				if r.URL.Path == "/api/v1/query" {
					at = rawAt
					q := r.URL.Query().Get("query")
					if !strings.HasPrefix(q, `ALERTS{alertstate="firing"}[`) || !strings.HasSuffix(q, "]") {
						t.Errorf("raw range selector required: %q", q)
					}
					if err := validPromQL(q); err != nil {
						t.Error(err)
					}
					// Model the instant range selector's open lower bound. This also proves
					// the request covers the full window and preserves its inclusive start.
					seconds, err := strconv.ParseFloat(r.URL.Query().Get("time"), 64)
					if err != nil {
						t.Error(err)
						http.Error(w, "bad time", 400)
						return
					}
					width, err := ParseSpan(q[strings.LastIndex(q, "[")+1 : len(q)-1])
					if err != nil {
						t.Error(err)
						http.Error(w, "bad window", 400)
						return
					}
					end := time.Unix(int64(seconds), 0)
					if !rawAt.After(end.Add(-width)) || rawAt.After(end) {
						fmt.Fprint(w, `{"status":"success","data":{"resultType":"matrix","result":[]}}`)
						return
					}
				}
				fmt.Fprintf(w, `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"alertname":"High error rate","grafana_rule_uid":"high-errors"},"values":[[%s,"1"]]}]}}`, strconv.FormatFloat(float64(at.UnixMilli())/1000, 'f', 3, 64))
			}))
			defer srv.Close()
			win := Window{From: t0.Add(55 * time.Second), To: t0.Add(65 * time.Second)}
			res, err := Incidents(context.Background(), &Client{}, &Target{Metrics: srv.URL}, IncidentOptions{Dir: writeRepo(t), Since: time.Hour, Windows: []Window{win, {From: rawAt, To: rawAt.Add(time.Second)}}, Now: func() time.Time { return t0.Add(2 * time.Minute) }})
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Windows) != 2 || res.Windows[0].Status != "not_seen" || len(res.Windows[0].Fired) != 0 || res.Windows[1].Status != "caught" {
				t.Fatalf("a carried-forward query point is not a raw firing observation: %+v", res.Windows)
			}
			if len(res.Fired) != 1 || res.Fired[0].TimeSource != "query_evaluation" || len(res.Fired[0].Observations) != 1 || !res.Fired[0].Observations[0].At.Equal(rawAt) {
				t.Fatalf("preserve the underlying sample timestamp, including milliseconds: %+v", res.Fired)
			}
			assertReadOnly(t, rec)
		})
	}
}

func TestIncidentsRepoTitleUniquenessPrecedesAnyInferredUID(t *testing.T) {
	dir := writeRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "alerts", "checkout", "warning.yaml"), []byte(fmt.Sprintf(alertCR, "checkout-warning", "slow-charges", "High error rate", "")), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := grafanaWithCurrent(t, "["+historyRow("", 41, "High error rate", "Normal", "Alerting", t0, map[string]string{}, nil)+"]", currentRulesBody(false))
	res, err := Incidents(context.Background(), &Client{}, &Target{Grafana: srv.URL}, IncidentOptions{Dir: dir, Since: time.Hour, Now: func() time.Time { return t0.Add(time.Hour) }})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range res.Fired {
		if f.InRepo {
			t.Fatalf("an inferred UID must never bypass duplicate repo titles: %+v", res.Fired)
		}
	}
}

func TestIncidentsFutureWindowIsNotYetObservable(t *testing.T) {
	srv := grafanaFake(t, &recorder{}, "[]", 0)
	w := Window{From: t0, To: t0.Add(2 * time.Hour)}
	res, err := Incidents(context.Background(), &Client{}, &Target{Grafana: srv.URL}, IncidentOptions{Dir: writeRepo(t), Since: time.Hour, Windows: []Window{w}, Now: func() time.Time { return t0.Add(time.Hour) }})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Windows) != 1 || res.Windows[0].Status != "not_yet_observable" || !strings.Contains(res.Windows[0].Unknown, "not yet observable") {
		t.Fatalf("future windows must say they are not yet observable: %+v", res.Windows)
	}
	for _, m := range res.IncidentNotAlerted {
		if m.Incident == "" {
			t.Fatal("future window cannot produce a missed-incident suggestion")
		}
	}
}

func TestIncidentsRecoveringAtWindowEndPreservesPreviousEvidence(t *testing.T) {
	w := Window{From: t0, To: t0.Add(20 * time.Minute)}
	srv := grafanaFake(t, &recorder{}, "["+historyRow("high-errors", 7, "High error rate", "Alerting", "Recovering", w.To, nil, []string{"instance:a"})+"]", 0)
	res, err := Incidents(context.Background(), &Client{}, &Target{Grafana: srv.URL}, IncidentOptions{Dir: writeRepo(t), Since: time.Hour, Windows: []Window{w}, Now: func() time.Time { return t0.Add(time.Hour) }})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Windows) != 1 || res.Windows[0].Status != "caught" || len(res.Windows[0].Fired) != 1 || res.Windows[0].Fired[0] != "high-errors" {
		t.Fatalf("Alerting -> Recovering proves firing just before the window end as well as after it: %+v", res.Windows)
	}
}

// This is valid JSON larger than the HTTP client cap, without allocating a huge fixture.
func largeAlertsAnswer(w http.ResponseWriter) {
	fmt.Fprint(w, `{"status":"success","data":{"resultType":"matrix","result":[`)
	padding := strings.Repeat(" ", 64<<10)
	for i := 0; i < maxBody/len(padding)+1; i++ {
		if _, err := fmt.Fprint(w, padding); err != nil {
			return
		}
	}
	fmt.Fprint(w, `{"metric":{"alertname":"High error rate","grafana_rule_uid":"high-errors"},"values":[[1,"1"]]}]}}`)
}

func TestIncidentsAlertsLookbackStaysGrouped(t *testing.T) {
	var rawReads atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/query" {
			rawReads.Add(1)
			largeAlertsAnswer(w)
			return
		}
		if r.URL.Query().Get("query") != `max by (alertname, grafana_rule_uid) (ALERTS{alertstate="firing"})` {
			t.Errorf("lookback must stay grouped: %s", r.URL.RawQuery)
		}
		fmt.Fprintf(w, `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"alertname":"High error rate","grafana_rule_uid":"high-errors"},"values":[[%d,"1"]]}]}}`, t0.Unix())
	}))
	defer srv.Close()
	res, err := Incidents(context.Background(), &Client{}, &Target{Metrics: srv.URL}, IncidentOptions{Dir: writeRepo(t), Since: 7 * 24 * time.Hour, Now: func() time.Time { return t0.Add(time.Hour) }})
	if err != nil {
		t.Fatal(err)
	}
	if rawReads.Load() != 0 || len(res.Fired) != 1 || len(res.Fired[0].Observations) != 0 {
		t.Fatalf("no named windows need no raw reads: reads=%d result=%+v", rawReads.Load(), res)
	}
}

func TestIncidentsOversizedRawWindowIsUnknown(t *testing.T) {
	var rawReads atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/query" {
			rawReads.Add(1)
			largeAlertsAnswer(w)
			return
		}
		fmt.Fprint(w, `{"status":"success","data":{"resultType":"matrix","result":[]}}`)
	}))
	defer srv.Close()
	res, err := Incidents(context.Background(), &Client{}, &Target{Metrics: srv.URL}, IncidentOptions{Dir: writeRepo(t), Since: 7 * 24 * time.Hour, Windows: []Window{{From: t0, To: t0.Add(2 * time.Hour)}}, Now: func() time.Time { return t0.Add(3 * time.Hour) }})
	if err != nil {
		t.Fatal(err)
	}
	if rawReads.Load() != 1 || len(res.Windows) != 1 || res.Windows[0].Status != "not_seen" || len(res.Windows[0].Fired) != 0 || res.Windows[0].Unknown != "raw ALERTS samples too large to read" {
		t.Fatalf("oversized raw samples must leave only the window unknown: reads=%d %+v", rawReads.Load(), res.Windows)
	}
}

func TestIncidentsRawWindowsChunkAndMergeSamples(t *testing.T) {
	var rawReads atomic.Int64
	samples := []time.Time{t0.Add(10 * time.Minute), t0.Add(70 * time.Minute)}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/query_range" {
			fmt.Fprint(w, `{"status":"success","data":{"resultType":"matrix","result":[]}}`)
			return
		}
		rawReads.Add(1)
		q := r.URL.Query().Get("query")
		width, err := ParseSpan(q[strings.LastIndex(q, "[")+1 : len(q)-1])
		if err != nil || width > time.Hour+time.Millisecond {
			t.Errorf("raw read must stay bounded: %s", q)
		}
		seconds, _ := strconv.ParseFloat(r.URL.Query().Get("time"), 64)
		end := time.UnixMilli(int64(seconds * 1000))
		var points []string
		for _, at := range samples {
			if at.After(end.Add(-width)) && !at.After(end) {
				points = append(points, fmt.Sprintf(`[%d,"1"]`, at.Unix()))
			}
		}
		fmt.Fprintf(w, `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"grafana_rule_uid":"high-errors"},"values":[%s]}]}}`, strings.Join(points, ","))
	}))
	defer srv.Close()
	res, err := Incidents(context.Background(), &Client{}, &Target{Metrics: srv.URL}, IncidentOptions{Dir: writeRepo(t), Since: 7 * 24 * time.Hour, Windows: []Window{{From: t0, To: t0.Add(2 * time.Hour)}}, Now: func() time.Time { return t0.Add(3 * time.Hour) }})
	if err != nil {
		t.Fatal(err)
	}
	if rawReads.Load() != 2 || len(res.Fired) != 1 || len(res.Fired[0].Observations) != 2 || !res.Fired[0].Observations[0].At.Equal(samples[0]) || !res.Fired[0].Observations[1].At.Equal(samples[1]) || res.Windows[0].Status != "caught" {
		t.Fatalf("merge timestamped evidence from bounded chunks: reads=%d %+v", rawReads.Load(), res)
	}
}

func TestIncidentsFutureOversizedChunkPreservesPartialEvidenceAndReason(t *testing.T) {
	var rawReads atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/query_range" {
			fmt.Fprint(w, `{"status":"success","data":{"resultType":"matrix","result":[]}}`)
			return
		}
		if rawReads.Add(1) == 1 {
			fmt.Fprintf(w, `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"grafana_rule_uid":"high-errors"},"values":[[%d,"1"]]}]}}`, t0.Add(15*time.Minute).Unix())
			return
		}
		largeAlertsAnswer(w)
	}))
	defer srv.Close()
	res, err := Incidents(context.Background(), &Client{}, &Target{Metrics: srv.URL}, IncidentOptions{Dir: writeRepo(t), Since: 7 * 24 * time.Hour, Windows: []Window{{From: t0, To: t0.Add(2 * time.Hour)}}, Now: func() time.Time { return t0.Add(90 * time.Minute) }})
	if err != nil {
		t.Fatal(err)
	}
	if rawReads.Load() != 2 || len(res.Windows) != 1 {
		t.Fatalf("expected one partial window and two raw chunks: reads=%d %+v", rawReads.Load(), res.Windows)
	}
	wr := res.Windows[0]
	if wr.Status != "not_yet_observable" || len(wr.Fired) != 0 || len(wr.PartialFired) != 1 || wr.PartialFired[0] != "high-errors" || !strings.Contains(wr.Unknown, "window ends after the history cutoff") || !strings.Contains(wr.Unknown, "raw ALERTS samples too large to read") {
		t.Fatalf("keep verified partial firing and both uncertainty reasons: %+v", wr)
	}
	var out strings.Builder
	res.Print(&out)
	if !strings.Contains(out.String(), "partial evidence before the cutoff: [high-errors]") || !strings.Contains(out.String(), "raw ALERTS samples too large to read") || strings.Contains(out.String(), "caught:") {
		t.Fatalf("text must show partial evidence and the size diagnostic without caught: %s", out.String())
	}
}

// A firing whose resolution is missing from history is an open span inferred to the cutoff:
// its duration is unknown, never a measured 55 minutes. A span with both transitions is measured.
func TestIncidentsFiringWithoutResolutionIsOpenNotMeasured(t *testing.T) {
	dir := writeRepo(t)
	now := t0.Add(time.Hour)
	open := grafanaHistory(t, &recorder{}, []stateChange{{"high-errors", "High error rate", "Normal", "Alerting", t0.Add(5 * time.Minute)}})
	res, err := Incidents(context.Background(), &Client{}, &Target{Name: "prod", Grafana: open.URL}, IncidentOptions{Dir: dir, Since: 2 * time.Hour, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Fired) != 1 {
		t.Fatalf("the firing observation is kept: %+v", res.Fired)
	}
	f := res.Fired[0]
	if f.Firings != 1 || !f.First.Equal(t0.Add(5*time.Minute)) || len(f.Observations) == 0 {
		t.Fatalf("keep the observed firing at 14:05: %+v", f)
	}
	if !f.SpanOpen || f.Longest != 0 {
		t.Fatalf("a firing with no resolution in history is open with unknown duration, not a measured %s: %+v", f.Longest, f)
	}
	var sb strings.Builder
	res.Print(&sb)
	if strings.Contains(sb.String(), "longest 55m") || !strings.Contains(sb.String(), "no resolution in history") {
		t.Fatalf("the text must say the span is open and inferred:\n%s", sb.String())
	}
	if b, err := res.JSON(); err != nil || !strings.Contains(string(b), `"span_open": true`) {
		t.Fatalf("json must carry span_open: %v %s", err, b)
	}

	closed := grafanaHistory(t, &recorder{}, []stateChange{
		{"high-errors", "High error rate", "Normal", "Alerting", t0.Add(5 * time.Minute)},
		{"high-errors", "High error rate", "Alerting", "Normal", t0.Add(6 * time.Minute)},
	})
	res, err = Incidents(context.Background(), &Client{}, &Target{Name: "prod", Grafana: closed.URL}, IncidentOptions{Dir: dir, Since: 2 * time.Hour, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Fired) != 1 || res.Fired[0].SpanOpen || res.Fired[0].Longest != time.Minute {
		t.Fatalf("both transitions present: the measured duration stays: %+v", res.Fired)
	}
}

// seriesServer answers the grouped lookback with rows and the raw read with the raw rows.
func seriesServer(t *testing.T, grouped, raw string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/query" {
			fmt.Fprintf(w, `{"status":"success","data":{"resultType":"matrix","result":[%s]}}`, raw)
			return
		}
		fmt.Fprintf(w, `{"status":"success","data":{"resultType":"matrix","result":[%s]}}`, grouped)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// A ruler UID and an alert name with the same text are different grouping keys.
func TestIncidentsUIDAndAlertNameWithTheSameTextStayApart(t *testing.T) {
	grouped := fmt.Sprintf(`{"metric":{"alertname":"High error rate","grafana_rule_uid":"high-errors"},"values":[[%d,"1"]]},{"metric":{"alertname":"high-errors"},"values":[[%d,"1"]]}`, t0.Unix(), t0.Unix())
	// Only the name-only alert has a raw firing sample inside the named window.
	raw := fmt.Sprintf(`{"metric":{"alertname":"high-errors"},"values":[[%d,"1"]]}`, t0.Add(10*time.Minute).Unix())
	srv := seriesServer(t, grouped, raw)
	res, err := Incidents(context.Background(), &Client{}, &Target{Metrics: srv.URL}, IncidentOptions{Dir: writeRepo(t), Since: 7 * 24 * time.Hour, Windows: []Window{{From: t0, To: t0.Add(time.Hour)}}, Now: func() time.Time { return t0.Add(3 * time.Hour) }})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Fired) != 2 {
		t.Fatalf("the UID row and the name-only row are two alerts: %+v", res.Fired)
	}
	for _, f := range res.Fired {
		switch {
		case f.InRepo && len(f.Observations) != 0:
			t.Fatalf("the sample of the name-only alert must not make the UID alert catch the window: %+v", f)
		case !f.InRepo && len(f.Observations) != 1:
			t.Fatalf("the name-only alert owns the raw sample: %+v", f)
		}
	}
	// The typed identity reaches the named window: the name-only alert is shown as alertname:<name>
	// wherever it appears, so the window cannot be read as the UID alert having caught it.
	ids := map[string]bool{}
	for _, f := range res.Fired {
		ids[f.ID] = true
	}
	if !ids["high-errors"] || !ids["alertname:high-errors"] || len(ids) != 2 {
		t.Fatalf("fired ids must tell the UID row from the name-only row: %+v", ids)
	}
	if len(res.Windows) != 1 || len(res.Windows[0].Fired) != 1 || res.Windows[0].Fired[0] != "alertname:high-errors" {
		t.Fatalf("windows[].fired must name the name-only alert: %+v", res.Windows)
	}
	var out strings.Builder
	res.Print(&out)
	if !strings.Contains(out.String(), "caught: alertname:high-errors fired") || strings.Contains(out.String(), "caught: high-errors fired") {
		t.Fatalf("the printed caught line must carry the typed identity:\n%s", out.String())
	}
	if b, err := res.JSON(); err != nil || !strings.Contains(string(b), `"id": "alertname:high-errors"`) {
		t.Fatalf("json fired[].id must carry it too: %v %s", err, b)
	}
}

// Swapping which of the two same-text alerts fired inside the window swaps the attribution.
func TestIncidentsSwappingWhichSameTextAlertFiredChangesTheWindowAttribution(t *testing.T) {
	grouped := fmt.Sprintf(`{"metric":{"alertname":"High error rate","grafana_rule_uid":"high-errors"},"values":[[%d,"1"]]},{"metric":{"alertname":"high-errors"},"values":[[%d,"1"]]}`, t0.Unix(), t0.Unix())
	opts := IncidentOptions{Dir: writeRepo(t), Since: 7 * 24 * time.Hour, Windows: []Window{{From: t0, To: t0.Add(time.Hour)}}, Now: func() time.Time { return t0.Add(3 * time.Hour) }}
	for name, c := range map[string]struct{ raw, want, notWant string }{
		"name-only fired": {fmt.Sprintf(`{"metric":{"alertname":"high-errors"},"values":[[%d,"1"]]}`, t0.Add(10*time.Minute).Unix()), "alertname:high-errors", "high-errors"},
		"uid fired":       {fmt.Sprintf(`{"metric":{"alertname":"High error rate","grafana_rule_uid":"high-errors"},"values":[[%d,"1"]]}`, t0.Add(10*time.Minute).Unix()), "high-errors", "alertname:high-errors"},
	} {
		srv := seriesServer(t, grouped, c.raw)
		res, err := Incidents(context.Background(), &Client{}, &Target{Metrics: srv.URL}, opts)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Windows) != 1 || len(res.Windows[0].Fired) != 1 || res.Windows[0].Fired[0] != c.want {
			t.Fatalf("%s: window attribution want %q: %+v", name, c.want, res.Windows)
		}
		var out strings.Builder
		res.Print(&out)
		if !strings.Contains(out.String(), "caught: "+c.want+" fired") || (c.notWant == "alertname:high-errors" && strings.Contains(out.String(), "caught: alertname:")) {
			t.Fatalf("%s: text attribution:\n%s", name, out.String())
		}
	}
}

// With no collision the name-only alert keeps its plain display ID (old output unchanged).
func TestIncidentsNameOnlyAlertWithoutCollisionKeepsPlainID(t *testing.T) {
	grouped := fmt.Sprintf(`{"metric":{"alertname":"high-errors"},"values":[[%d,"1"]]}`, t0.Unix())
	srv := seriesServer(t, grouped, "")
	res, err := Incidents(context.Background(), &Client{}, &Target{Metrics: srv.URL}, IncidentOptions{Dir: writeRepo(t), Since: 7 * 24 * time.Hour, Now: func() time.Time { return t0.Add(time.Hour) }})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Fired) != 1 || res.Fired[0].ID != "high-errors" {
		t.Fatalf("%+v", res.Fired)
	}
}

// An alert name that equals a repository UID is not that rule: only an explicit UID, or a unique title, matches.
func TestIncidentsAlertNameEqualToRepoUIDDoesNotMatchTheRule(t *testing.T) {
	grouped := fmt.Sprintf(`{"metric":{"alertname":"high-errors"},"values":[[%d,"1"]]}`, t0.Unix())
	srv := seriesServer(t, grouped, "")
	res, err := Incidents(context.Background(), &Client{}, &Target{Metrics: srv.URL}, IncidentOptions{Dir: writeRepo(t), Since: 7 * 24 * time.Hour, Now: func() time.Time { return t0.Add(time.Hour) }})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Fired) != 1 {
		t.Fatalf("%+v", res.Fired)
	}
	f := res.Fired[0]
	if f.InRepo || f.Runbook != "" || f.Incident != "" {
		t.Fatalf("a name-only row called high-errors is not the rule with uid high-errors (title High error rate): %+v", f)
	}
	if len(res.FiredNoIncident) != 1 {
		t.Fatalf("it is still fired-but-no-incident: %+v", res.FiredNoIncident)
	}

	// The explicit UID and a unique title still match.
	for name, metric := range map[string]string{
		"uid":   `{"alertname":"anything","grafana_rule_uid":"high-errors"}`,
		"title": `{"alertname":"High error rate"}`,
	} {
		srv := seriesServer(t, fmt.Sprintf(`{"metric":%s,"values":[[%d,"1"]]}`, metric, t0.Unix()), "")
		res, err := Incidents(context.Background(), &Client{}, &Target{Metrics: srv.URL}, IncidentOptions{Dir: writeRepo(t), Since: 7 * 24 * time.Hour, Now: func() time.Time { return t0.Add(time.Hour) }})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Fired) != 1 || !res.Fired[0].InRepo || res.Fired[0].ID != "high-errors" || res.Fired[0].Runbook == "" || res.Fired[0].Incident != "spike" {
			t.Fatalf("%s match: %+v", name, res.Fired)
		}
	}
}

// twoInstanceHistory builds the history of one rule with two instances (label maps): A and B.
func twoInstanceHistory(rows ...string) string { return "[" + strings.Join(rows, ",") + "]" }

func incidentsOver(t *testing.T, body string, now time.Time) FiredAlert {
	t.Helper()
	srv := grafanaFake(t, &recorder{}, body, 0)
	res, err := Incidents(context.Background(), &Client{}, &Target{Name: "prod", Grafana: srv.URL}, IncidentOptions{Dir: writeRepo(t), Since: 2 * time.Hour, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Fired) != 1 {
		t.Fatalf("one rule fired: %+v", res.Fired)
	}
	return res.Fired[0]
}

// Merging the spans of several instances of one rule must not lose evidence: the union is open when ANY
// instance span is open, and longest_ns is the longest measured (closed) single-instance span.
func TestIncidentsMergedInstancesKeepOpenSpanAndLongestMeasured(t *testing.T) {
	a, b := map[string]string{"instance": "a"}, map[string]string{"instance": "b"}
	row := func(prev, next string, at time.Duration, l map[string]string) string {
		return historyRow("high-errors", 7, "High error rate", prev, next, t0.Add(at), l, nil)
	}
	now := t0.Add(time.Hour)

	// (a) A fires +5m resolves +15m (10m measured); B fires +10m and never resolves.
	f := incidentsOver(t, twoInstanceHistory(
		row("Normal", "Alerting", 5*time.Minute, a),
		row("Normal", "Alerting", 10*time.Minute, b),
		row("Alerting", "Normal", 15*time.Minute, a),
	), now)
	if !f.SpanOpen || f.Longest != 10*time.Minute || f.Firings != 1 {
		t.Fatalf("(a) one open instance keeps span_open, and the closed 10m span is the longest measured: %+v", f)
	}

	// (b) A resolves exactly at the cutoff end while B is still open: the inferred ends are equal.
	f = incidentsOver(t, twoInstanceHistory(
		row("Normal", "Alerting", 5*time.Minute, a),
		row("Normal", "Alerting", 10*time.Minute, b),
		row("Alerting", "Normal", 60*time.Minute, a),
	), now)
	if !f.SpanOpen || f.Longest != 55*time.Minute {
		t.Fatalf("(b) an open span whose inferred end equals a closed span's end stays open; longest is A's measured 55m: %+v", f)
	}

	// Both closed: not open, longest is the longer single-instance span even when they overlap.
	f = incidentsOver(t, twoInstanceHistory(
		row("Normal", "Alerting", 5*time.Minute, a),
		row("Normal", "Alerting", 10*time.Minute, b),
		row("Alerting", "Normal", 15*time.Minute, a),
		row("Alerting", "Normal", 30*time.Minute, b),
	), now)
	if f.SpanOpen || f.Longest != 20*time.Minute {
		t.Fatalf("all closed: not open, longest single instance 20m: %+v", f)
	}
}

// (c) mergeIntervals itself: deleting the open propagation must fail here.
func TestMergeIntervalsOpenIsTrueWhenAnyMergedSpanIsOpen(t *testing.T) {
	end := t0.Add(time.Hour)
	at := func(m int) time.Time { return t0.Add(time.Duration(m) * time.Minute) }
	cases := map[string][]interval{
		"open extends the union":         {{From: at(5), To: at(15)}, {From: at(10), To: end, open: true}},
		"open ends where closed ends":    {{From: at(5), To: end}, {From: at(10), To: end, open: true}},
		"open first, closed later":       {{From: at(5), To: end, open: true}, {From: at(10), To: at(20)}},
		"open ends inside a closed span": {{From: at(5), To: end, open: true}, {From: at(10), To: end}},
	}
	for name, spans := range cases {
		got := mergeIntervals(append([]interval(nil), spans...))
		if len(got) != 1 || !got[0].open {
			t.Errorf("%s: %+v", name, got)
		}
	}
	got := mergeIntervals([]interval{{From: at(5), To: at(15)}, {From: at(10), To: at(30)}})
	if len(got) != 1 || got[0].open || !got[0].To.Equal(at(30)) {
		t.Errorf("closed spans stay closed: %+v", got)
	}
}

// A span whose start was synthesized at the history read start (the first entry is Alerting -> Normal)
// is not measured: longest_ns counts only spans with BOTH transitions observed. The firing itself was
// observed, so the window is still caught.
func TestIncidentsSynthesizedStartIsNotMeasured(t *testing.T) {
	dir := writeRepo(t)
	now := t0.Add(time.Hour)
	srv := grafanaHistory(t, &recorder{}, []stateChange{{"high-errors", "High error rate", "Alerting", "Normal", t0.Add(40 * time.Minute)}})
	res, err := Incidents(context.Background(), &Client{}, &Target{Name: "prod", Grafana: srv.URL}, IncidentOptions{
		Dir: dir, Since: 30 * time.Minute, Now: func() time.Time { return now },
		Windows: []Window{{From: t0.Add(35 * time.Minute), To: t0.Add(45 * time.Minute)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Fired) != 1 {
		t.Fatalf("%+v", res.Fired)
	}
	f := res.Fired[0]
	if f.Longest != 0 || f.SpanOpen || !f.StartedBeforeWindow || f.Firings != 1 {
		t.Fatalf("the only span has an inferred start: longest_ns must be 0, not a measured duration: %+v", f)
	}
	if len(res.Windows) != 1 || res.Windows[0].Status != "caught" || len(res.Windows[0].Fired) != 1 {
		t.Fatalf("the direct observation still catches the window: %+v", res.Windows)
	}
	var sb strings.Builder
	res.Print(&sb)
	if strings.Contains(sb.String(), "longest 0s") || strings.Contains(sb.String(), "longest 10m") {
		t.Fatalf("the text must not claim a measured duration:\n%s", sb.String())
	}
	// A second instance that has both transitions observed is still measured.
	a, b := map[string]string{"instance": "a"}, map[string]string{"instance": "b"}
	row := func(prev, next string, at time.Duration, l map[string]string) string {
		return historyRow("high-errors", 7, "High error rate", prev, next, t0.Add(at), l, nil)
	}
	g := incidentsOver(t, twoInstanceHistory(
		row("Alerting", "Normal", 40*time.Minute, a),
		row("Normal", "Alerting", 41*time.Minute, b),
		row("Alerting", "Normal", 44*time.Minute, b),
	), now)
	if g.Longest != 3*time.Minute {
		t.Fatalf("the measured instance span stays; the synthesized one is ignored: %+v", g)
	}
}

// Every instance span is clipped to the report window [from, cutoff) and empty ones are dropped before
// merging: a firing exactly at the exclusive cutoff neither sets span_open nor merges.
func TestIncidentsFiringExactlyAtTheCutoffIsNotASpan(t *testing.T) {
	a, b := map[string]string{"instance": "a"}, map[string]string{"instance": "b"}
	row := func(prev, next string, at time.Duration, l map[string]string) string {
		return historyRow("high-errors", 7, "High error rate", prev, next, t0.Add(at), l, nil)
	}
	now := t0.Add(time.Hour)

	// A closed 10m span, then B starts exactly at the cutoff.
	f := incidentsOver(t, twoInstanceHistory(
		row("Normal", "Alerting", 5*time.Minute, a),
		row("Alerting", "Normal", 15*time.Minute, a),
		row("Normal", "Alerting", 60*time.Minute, b),
	), now)
	if f.SpanOpen || f.Firings != 1 || f.Longest != 10*time.Minute {
		t.Fatalf("an empty span at the cutoff is no firing and not open: %+v", f)
	}

	// A resolves exactly at the cutoff; B starts exactly at the cutoff: they must not merge into an open span.
	f = incidentsOver(t, twoInstanceHistory(
		row("Normal", "Alerting", 5*time.Minute, a),
		row("Alerting", "Normal", 60*time.Minute, a),
		row("Normal", "Alerting", 60*time.Minute, b),
	), now)
	if f.SpanOpen || f.Firings != 1 || f.Longest != 55*time.Minute {
		t.Fatalf("B at the cutoff must not merge with A or open the span: %+v", f)
	}

	// Round-2 behaviour stays: an open span that started before the cutoff still opens the union.
	f = incidentsOver(t, twoInstanceHistory(
		row("Normal", "Alerting", 5*time.Minute, a),
		row("Normal", "Alerting", 10*time.Minute, b),
		row("Alerting", "Normal", 60*time.Minute, a),
	), now)
	if !f.SpanOpen || f.Longest != 55*time.Minute {
		t.Fatalf("an open span before the cutoff keeps span_open: %+v", f)
	}
}

// Three fired identities whose text collides all get distinct display IDs, deterministically, in
// every output; swapping which one has the raw sample changes the attribution.
func TestIncidentsEveryFiredIdentityHasADistinctDisplayID(t *testing.T) {
	uidRow := `{"alertname":"High error rate","grafana_rule_uid":"high-errors"}`
	nameRow := `{"alertname":"high-errors"}`
	typedRow := `{"alertname":"alertname:high-errors"}`
	grouped := ""
	for _, m := range []string{uidRow, nameRow, typedRow} {
		grouped += fmt.Sprintf(`{"metric":%s,"values":[[%d,"1"]]},`, m, t0.Unix())
	}
	grouped = strings.TrimSuffix(grouped, ",")
	want := map[string]string{uidRow: "high-errors", nameRow: "alertname:high-errors#2", typedRow: "alertname:high-errors"}
	seenIDs := map[string]bool{}
	for _, c := range []struct {
		name, metric string
		cutoffAfter  bool
	}{{"uid", uidRow, false}, {"name-only", nameRow, false}, {"name-only typed", typedRow, false}, {"future uid", uidRow, true}, {"future name-only", nameRow, true}, {"future typed", typedRow, true}} {
		raw := fmt.Sprintf(`{"metric":%s,"values":[[%d,"1"]]}`, c.metric, t0.Add(10*time.Minute).Unix())
		srv := seriesServer(t, grouped, raw)
		w := Window{From: t0, To: t0.Add(time.Hour)}
		now := t0.Add(3 * time.Hour)
		if c.cutoffAfter {
			now = t0.Add(30 * time.Minute)
		}
		res, err := Incidents(context.Background(), &Client{}, &Target{Metrics: srv.URL}, IncidentOptions{Dir: writeRepo(t), Since: 7 * 24 * time.Hour, Windows: []Window{w}, Now: func() time.Time { return now }})
		if err != nil {
			t.Fatal(err)
		}
		ids := map[string]bool{}
		for _, f := range res.Fired {
			ids[f.ID] = true
		}
		if len(res.Fired) != 3 || len(ids) != 3 || !ids["high-errors"] || !ids["alertname:high-errors"] || !ids["alertname:high-errors#2"] {
			t.Fatalf("%s: three identities, three distinct ids: %+v", c.name, ids)
		}
		got := res.Windows[0].Fired
		if c.cutoffAfter {
			got = res.Windows[0].PartialFired
		}
		if len(got) != 1 || got[0] != want[c.metric] {
			t.Fatalf("%s: attribution want %q, got %v (%+v)", c.name, want[c.metric], got, res.Windows[0])
		}
		seenIDs[got[0]] = true
		var out strings.Builder
		res.Print(&out)
		if !c.cutoffAfter && !strings.Contains(out.String(), "caught: "+want[c.metric]+" fired") {
			t.Fatalf("%s: text:\n%s", c.name, out.String())
		}
		for id := range ids {
			if !strings.Contains(out.String(), "  "+id+": ") {
				t.Fatalf("%s: FIRED line for %q missing:\n%s", c.name, id, out.String())
			}
		}
		noInc := map[string]bool{}
		for _, f := range res.FiredNoIncident {
			noInc[f.ID] = true
		}
		if !noInc["alertname:high-errors#2"] || !noInc["alertname:high-errors"] {
			t.Fatalf("%s: fired-but-no-incident ids: %+v", c.name, noInc)
		}
		// Deterministic: a second run over the same input gives the same ids.
		again, err := Incidents(context.Background(), &Client{}, &Target{Metrics: srv.URL}, IncidentOptions{Dir: writeRepo(t), Since: 7 * 24 * time.Hour, Windows: []Window{w}, Now: func() time.Time { return now }})
		if err != nil {
			t.Fatal(err)
		}
		a, _ := res.JSON()
		b, _ := again.JSON()
		if string(a) != string(b) {
			t.Fatalf("%s: output is not deterministic", c.name)
		}
	}
	if len(seenIDs) != 3 {
		t.Fatalf("swapping the observation between identities must change the attribution: %v", seenIDs)
	}
}

// A firing and its resolution in the same millisecond leave no span after clipping, but the firing was
// observed: first and last come from the observation, while the span count and duration stay zero, and
// the text says no span was measured instead of printing a zero time.
func TestIncidentsZeroLengthFiringKeepsObservedTimes(t *testing.T) {
	a := map[string]string{"instance": "a"}
	at := t0.Add(5 * time.Minute)
	body := twoInstanceHistory(
		historyRow("high-errors", 7, "High error rate", "Normal", "Alerting", at, a, nil),
		historyRow("high-errors", 7, "High error rate", "Alerting", "Normal", at, a, nil),
	)
	srv := grafanaFake(t, &recorder{}, body, 0)
	now := t0.Add(time.Hour)
	res, err := Incidents(context.Background(), &Client{}, &Target{Name: "prod", Grafana: srv.URL}, IncidentOptions{Dir: writeRepo(t), Since: 2 * time.Hour, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Fired) != 1 {
		t.Fatalf("one rule fired: %+v", res.Fired)
	}
	f := res.Fired[0]
	if f.Firings != 0 || f.Longest != 0 || f.SpanOpen {
		t.Fatalf("no span survives a zero-length firing: %+v", f)
	}
	if !f.First.Equal(at) || !f.Last.Equal(at) {
		t.Fatalf("first/last come from the observation %s, got first %s last %s", at, f.First, f.Last)
	}
	var out strings.Builder
	res.Print(&out)
	if strings.Contains(out.String(), "0001-01-01") {
		t.Fatalf("text prints a zero time:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "high-errors: firing observed, first "+at.Format(time.RFC3339)+"; no span of nonzero length in history, so no firing count or duration is measured") {
		t.Fatalf("text names the observed firing without a span:\n%s", out.String())
	}
}

// On the ALERTS fallback a name-only row matches a repository rule by title only when no other fired
// identity carries that title: a second fired alert with the same title and a different, non-repository
// UID makes the title ambiguous, so neither row borrows the repository rule's runbook or incident.
func TestIncidentsTitleFallbackSkipsATitleAnotherFiredAlertShares(t *testing.T) {
	nameOnly := `{"alertname":"High error rate"}`
	otherUID := `{"alertname":"High error rate","grafana_rule_uid":"elsewhere"}`
	grouped := fmt.Sprintf(`{"metric":%s,"values":[[%d,"1"]]},{"metric":%s,"values":[[%d,"1"]]}`, nameOnly, t0.Unix(), otherUID, t0.Unix())
	raw := fmt.Sprintf(`{"metric":%s,"values":[[%d,"1"]]}`, nameOnly, t0.Add(10*time.Minute).Unix())
	srv := seriesServer(t, grouped, raw)
	res, err := Incidents(context.Background(), &Client{}, &Target{Metrics: srv.URL}, IncidentOptions{Dir: writeRepo(t), Since: 7 * 24 * time.Hour, Windows: []Window{{From: t0, To: t0.Add(time.Hour)}}, Now: func() time.Time { return t0.Add(3 * time.Hour) }})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Fired) != 2 {
		t.Fatalf("two fired identities: %+v", res.Fired)
	}
	for _, f := range res.Fired {
		if f.InRepo || f.ID == "high-errors" || f.Runbook != "" || f.Incident != "" {
			t.Fatalf("a shared title must not attribute a fired alert to the repository rule: %+v", f)
		}
	}
	if len(res.FiredNoIncident) != 2 {
		t.Fatalf("both fired alerts stay without an incident: %+v", res.FiredNoIncident)
	}

	// Uncontested: the name-only row alone still matches the repository rule by its unique title.
	grouped = fmt.Sprintf(`{"metric":%s,"values":[[%d,"1"]]}`, nameOnly, t0.Unix())
	srv = seriesServer(t, grouped, raw)
	res, err = Incidents(context.Background(), &Client{}, &Target{Metrics: srv.URL}, IncidentOptions{Dir: writeRepo(t), Since: 7 * 24 * time.Hour, Windows: []Window{{From: t0, To: t0.Add(time.Hour)}}, Now: func() time.Time { return t0.Add(3 * time.Hour) }})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Fired) != 1 || !res.Fired[0].InRepo || res.Fired[0].ID != "high-errors" {
		t.Fatalf("an uncontested title still matches: %+v", res.Fired)
	}
}
