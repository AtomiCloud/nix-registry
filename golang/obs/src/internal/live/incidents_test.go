package live

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	return fmt.Sprintf(`{"id":1,"alertId":7,"alertName":%q,"alertUID":%q,"prevState":%q,"newState":%q,"time":%d,"timeEnd":%d,"text":"x"}`, name, uid, prev, next, at.UnixMilli(), at.UnixMilli())
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

func TestIncidentsNamedWindowWithNoAlertIsNotAlerted(t *testing.T) {
	dir := writeRepo(t)
	rec := &recorder{}
	srv := grafanaFake(t, rec, "["+ann("high-errors", "High error rate", "Normal", "Alerting", t0)+","+ann("high-errors", "High error rate", "Alerting", "Normal", t0.Add(10*time.Minute))+"]", 0)
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
	if len(missed) != 1 || !missed[0].From.Equal(w2.From) {
		t.Fatalf("only the quiet window is a missed incident: %+v", res.IncidentNotAlerted)
	}
	var sb strings.Builder
	res.Print(&sb)
	if !strings.Contains(sb.String(), "fire: []") || !strings.Contains(sb.String(), "no alert caught it") {
		t.Fatalf("the missed window gets an entry the LLM can finish:\n%s", sb.String())
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
