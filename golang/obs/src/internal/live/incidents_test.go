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
	// every suggestion is text: nothing is written
	var sb strings.Builder
	res.Print(&sb)
	out := sb.String()
	for _, want := range []string{"fired-but-no-incident", "incident-not-alerted", "alert-without-runbook", "slow-charges", "orphan", "story:", "expect:", "fire: [slow-charges]", "writes nothing"} {
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
	var missed []Window
	for _, m := range res.IncidentNotAlerted {
		if m.Window != nil {
			missed = append(missed, *m.Window)
		}
	}
	if len(missed) != 0 || len(res.Windows) != 2 || res.Windows[1].Unknown == "" {
		t.Fatalf("a quiet-looking Grafana window is unknown, never a miss: %+v", res)
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
	if len(res.Fired) != 1 || res.Fired[0].ID != "high-errors" || res.Fired[0].Firings != 1 {
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
	if len(res.Fired) != 1 || res.Fired[0].Longest != 10*time.Minute || !res.Fired[0].StartedBeforeWindow {
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
		if m.Window != nil {
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
		if m.Window != nil {
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
		if m.Window != nil {
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
		if m.Window != nil {
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
		if m.Window != nil {
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
		if m.Window != nil {
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
