//go:build parity

package check

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

type parityAlertStep struct {
	T int64             `json:"t"`
	E map[string]string `json:"e"`
	V map[string]any    `json:"v"`
}
type parityAlertRun struct {
	Alert       string            `json:"alert"`
	Run         string            `json:"run"`
	Steps       []parityAlertStep `json:"steps"`
	Transitions []struct {
		T                          int64 `json:"t"`
		Instance, From, To, Reason string
	} `json:"transitions"`
}

func requireCommand(t *testing.T, command string) string {
	t.Helper()
	binary := os.Getenv("OBS_PARITY_OBS")
	if binary == "" {
		if _, err := os.Stat("../../cmd_" + command + ".go"); os.IsNotExist(err) {
			t.Skip("obs " + command + " has not landed")
		}
		t.Fatal("OBS_PARITY_OBS must point to the freshly built obs binary")
	}
	help, err := exec.Command(binary, "help").CombinedOutput()
	if err != nil {
		t.Fatalf("obs help: %v: %s", err, help)
	}
	if !strings.Contains(string(help), "obs "+command+" ") {
		if _, err := os.Stat("../../cmd_" + command + ".go"); err == nil {
			t.Fatalf("obs %s source exists but the freshly built binary does not advertise it: %s", command, help)
		}
		t.Skip("obs " + command + " has not landed in the tested binary")
	}
	return binary
}

func writeParityJSON(t *testing.T, path string, value any) {
	t.Helper()
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0644); err != nil {
		t.Fatal(err)
	}
}

func parityEvidence(t *testing.T, name string, value any) {
	t.Helper()
	if state := os.Getenv("OBS_PARITY_STATE"); state != "" {
		writeParityJSON(t, filepath.Join(state, strings.ReplaceAll(t.Name(), "/", "-")+"-"+name+".json"), value)
	}
}

func instantAlertData(expr, expression string) []any {
	return []any{
		map[string]any{"refId": "A", "datasourceUid": "parity-metrics", "relativeTimeRange": map[string]int{"from": 600, "to": 0}, "model": map[string]any{"refId": "A", "expr": expr, "instant": true, "range": false, "datasource": map[string]string{"type": "prometheus", "uid": "parity-metrics"}}},
		map[string]any{"refId": "C", "datasourceUid": "__expr__", "relativeTimeRange": map[string]int{"from": 0, "to": 0}, "model": map[string]any{"refId": "C", "type": "math", "expression": expression}},
	}
}

func alertProject(t *testing.T, length, interval string, rules []map[string]any) string {
	t.Helper()
	dir := t.TempDir()
	sd := "length: " + length + "\nrandom_seed: 42\nresource: {durian.cloud.service: parity}\ntraffic: {requests: 1/s}\nmetrics:\n  parity.requests:\n    type: counter\n    series: [{where: {outcome: error}, share: 1}]\nlogs:\n  - {event: parity.failed, level: error, from: {metric: parity.requests, where: {outcome: error}}}\n"
	if err := os.WriteFile(filepath.Join(dir, "seed.yaml"), []byte(sd), 0644); err != nil {
		t.Fatal(err)
	}
	var crRules []map[string]any
	for _, rule := range rules {
		cr := map[string]any{}
		for key, value := range rule {
			cr[key] = value
		}
		// The operator CR uses camelCase; Grafana's HTTP provisioning API uses
		// keep_firing_for. Both represent exactly the same duration.
		if value, ok := cr["keep_firing_for"]; ok {
			cr["keepFiringFor"] = value
			delete(cr, "keep_firing_for")
		}
		crRules = append(crRules, cr)
	}
	writeParityJSON(t, filepath.Join(dir, "alerts/parity/warning.yaml"), map[string]any{"apiVersion": "grafana.integreatly.org/v1beta1", "kind": "GrafanaAlertRuleGroup", "metadata": map[string]string{"name": "parity"}, "spec": map[string]any{"interval": interval, "rules": crRules}})
	return dir
}

func obsAlertSteps(t *testing.T, binary, dir string, base int64, extra ...string) []parityAlertRun {
	t.Helper()
	stepfile := filepath.Join(t.TempDir(), "steps.json")
	args := []string{"alerts", "--dir", dir, "--base", strconv.FormatInt(base, 10), "--steps", stepfile}
	cmd := exec.Command(binary, append(args, extra...)...)
	output, err := cmd.CombinedOutput()
	// A firing baseline intentionally violates the app's quiet-baseline expectation;
	// the parity verdict comes from every e/v/s below, not the CLI's app verdict.
	if err != nil {
		if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 {
			t.Fatalf("obs alerts: %v\n%s", err, output)
		}
	}
	b, err := os.ReadFile(stepfile)
	if err != nil {
		t.Fatalf("obs steps missing: %v\n%s", err, output)
	}
	var runs []parityAlertRun
	if err := json.Unmarshal(b, &runs); err != nil {
		t.Fatal(err)
	}
	if len(runs) == 0 {
		t.Fatalf("obs alerts emitted no runs: %s", output)
	}
	parityEvidence(t, "obs-steps", runs)
	return runs
}

func TestParityAlertEval(t *testing.T) {
	requireGrafana(t)
	binary := requireCommand(t, "alerts")
	rules := []map[string]any{
		{"uid": "parity-math", "title": "parity math", "condition": "C", "for": "1m", "data": instantAlertData("vector(time() % 240)", "$A > 120")},
		{"uid": "parity-normal", "title": "parity normal", "condition": "C", "for": "0s", "data": instantAlertData("vector(1)", "$A > 2")},
	}
	event := "parity.failed." + strconv.FormatInt(time.Now().UnixNano(), 10)
	// Keep the numeric stats value as the condition so the parity verdict checks
	// actual alert query counts as well as their instance states and labels.
	logData := instantAlertData(`"durian.cloud/event":=`+strconv.Quote(event)+` | stats by (outcome) count() n`, "$A")
	logQuery := logData[0].(map[string]any)
	logQuery["datasourceUid"] = "parity-logs"
	model := logQuery["model"].(map[string]any)
	model["datasource"] = map[string]string{"type": "victoriametrics-logs-datasource", "uid": "parity-logs"}
	model["queryType"] = "stats"
	delete(model, "instant")
	delete(model, "range")
	rules = append(rules, map[string]any{"uid": "parity-log-alert", "title": "parity log alert", "condition": "C", "for": "1m", "data": logData})
	dir := alertProject(t, "8m", "1m", rules)
	seedFile := filepath.Join(dir, "seed.yaml")
	seedBody, err := os.ReadFile(seedFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(seedFile, []byte(strings.ReplaceAll(string(seedBody), "parity.failed", event)), 0644); err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Truncate(time.Minute).Add(-66 * time.Hour).Unix()
	ds, err := makeData(Options{Dir: dir, Base: base})
	if err != nil {
		t.Fatal(err)
	}
	loadReal(t, os.Getenv("OBS_PARITY_VM"), os.Getenv("OBS_PARITY_VL"), ds)
	runs := obsAlertSteps(t, binary, dir, base)
	byUID := map[string]map[string]any{}
	for _, rule := range rules {
		byUID[rule["uid"].(string)] = rule
	}
	count := 0
	var evidence []json.RawMessage
	defer func() { parityEvidence(t, "grafana-eval", evidence) }()
	for _, run := range runs {
		rule := byUID[run.Alert]
		if rule == nil {
			t.Fatalf("unknown obs rule %s", run.Alert)
		}
		for _, step := range run.Steps {
			body, _ := json.Marshal(map[string]any{"condition": "C", "now": time.UnixMilli(step.T).UTC().Format(time.RFC3339Nano), "data": rule["data"]})
			response := postReal(t, os.Getenv("OBS_PARITY_GRAFANA")+"/api/v1/eval", "application/json", bytes.NewReader(body))
			evidence = append(evidence, json.RawMessage(response))
			var result struct {
				Results map[string]struct {
					Error  string         `json:"error"`
					Frames []grafanaFrame `json:"frames"`
				} `json:"results"`
			}
			if err := json.Unmarshal(response, &result); err != nil {
				t.Fatal(err)
			}
			condition, ok := result.Results["C"]
			if !ok || condition.Error != "" {
				t.Fatalf("%s minute %d eval: %s", run.Alert, step.T, response)
			}
			expectedStates := map[string]string{}
			expectedValues := map[string][]float64{}
			source := result.Results["A"]
			if len(source.Frames) == 0 || (len(source.Frames) == 1 && len(source.Frames[0].Schema.Fields) == 0) {
				datasource := rule["data"].([]any)[0].(map[string]any)["datasourceUid"].(string)
				key, _ := json.Marshal(map[string]string{"datasource_uid": datasource, "ref_id": "A"})
				expectedStates[string(key)] = "NoData"
			}
			for _, frame := range condition.Frames {
				for i, field := range frame.Schema.Fields {
					if field.Type != "number" {
						continue
					}
					labels := field.Labels
					if labels == nil {
						labels = map[string]string{}
					}
					keyBytes, _ := json.Marshal(labels)
					key := string(keyBytes)
					if len(frame.Data.Values[i]) != 1 {
						t.Fatalf("%s condition frame is not an instant scalar: %s", run.Alert, response)
					}
					value, ok := frame.Data.Values[i][0].(float64)
					if !ok {
						t.Fatalf("%s invalid condition value: %s", run.Alert, response)
					}
					state := "Normal"
					if value != 0 {
						state = "Alerting"
					}
					expectedStates[key] = state
					expectedValues[key] = []float64{value}
				}
			}
			if len(expectedStates) == 0 || !reflect.DeepEqual(step.E, expectedStates) {
				t.Fatalf("alerts/parity/warning.yaml rule %s minute %d: obs states %v, Grafana %v (%s)", run.Alert, step.T, step.E, expectedStates, response)
			}
			got := map[string][]float64{}
			for key, value := range step.V {
				n, ok := value.(float64)
				if !ok {
					t.Fatalf("invalid obs condition %v", value)
				}
				got[key] = []float64{n}
			}
			if err := compareNumbers(got, expectedValues); err != nil {
				t.Fatalf("rule %s minute %d: %v", run.Alert, step.T, err)
			}
			count++
		}
	}
	if count == 0 {
		t.Fatal("no historic alert evaluations")
	}
	t.Logf("MEASURED %d per-minute alert condition states and values against Grafana /api/v1/eval", count)
}

func grafanaRequest(t *testing.T, method, endpoint string, value any) []byte {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(method, os.Getenv("OBS_PARITY_GRAFANA")+endpoint, bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Disable-Provenance", "true")
	response, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode/100 != 2 {
		t.Fatalf("Grafana %s %s: HTTP %d: %s", method, endpoint, response.StatusCode, body)
	}
	return body
}

func TestParityAlertHistory(t *testing.T) {
	requireGrafana(t)
	binary := requireCommand(t, "alerts")
	created := time.Now().UTC()
	folder := "obs-parity-" + strconv.FormatInt(created.Unix(), 10)
	// An absolute-time pulse lets the real scheduler see Normal -> Pending ->
	// Alerting -> Recovering -> Normal without admitting future seed data to obs.
	on := created.Add(35 * time.Second).Unix()
	off := created.Add(75 * time.Second).Unix()
	rule := map[string]any{"uid": "parity-live", "title": "parity live", "condition": "C", "for": "20s", "keep_firing_for": "20s", "noDataState": "NoData", "execErrState": "Error", "folderUID": folder, "ruleGroup": "parity", "data": instantAlertData(fmt.Sprintf("vector((time() >= bool %d) * (time() < bool %d))", on, off), "$A > 0")}
	metric := "obs_parity_recorded_" + strconv.FormatInt(created.Unix(), 10)
	record := map[string]any{"uid": "parity-record", "title": "parity recording", "condition": "A", "folderUID": folder, "ruleGroup": "parity", "data": instantAlertData("vector(7)", "$A")[0:1], "record": map[string]string{"metric": metric, "from": "A", "target_datasource_uid": "parity-metrics"}}
	grafanaRequest(t, http.MethodPost, "/api/folders", map[string]string{"uid": folder, "title": folder})
	grafanaRequest(t, http.MethodPut, "/api/v1/provisioning/folder/"+folder+"/rule-groups/parity", map[string]any{"title": "parity", "folderUid": folder, "interval": 10, "rules": []any{rule, record}})
	defer grafanaRequest(t, http.MethodDelete, "/api/v1/provisioning/alert-rules/parity-live", nil)
	defer grafanaRequest(t, http.MethodDelete, "/api/v1/provisioning/alert-rules/parity-record", nil)
	var first int64
	var firstRecord int64
	deadline := created.Add(120 * time.Second)
	for time.Now().Before(deadline) {
		var result struct {
			Data struct {
				Groups []struct {
					Rules []struct{ Name, LastEvaluation, Health, LastError string } `json:"rules"`
				} `json:"groups"`
			} `json:"data"`
		}
		body := get(t, os.Getenv("OBS_PARITY_GRAFANA")+"/api/prometheus/grafana/api/v1/rules")
		if err := json.Unmarshal(body, &result); err != nil {
			t.Fatal(err)
		}
		for _, group := range result.Data.Groups {
			for _, r := range group.Rules {
				if r.Name != "parity live" && r.Name != "parity recording" {
					continue
				}
				if r.Health == "error" || r.LastError != "" {
					t.Fatalf("live rule health %s: %s", r.Health, r.LastError)
				}
				at, err := time.Parse(time.RFC3339Nano, r.LastEvaluation)
				if err == nil && at.After(created) {
					if r.Name == "parity live" && first == 0 {
						first = at.Unix() / 10 * 10
					}
					if r.Name == "parity recording" && firstRecord == 0 {
						firstRecord = at.Unix() / 10 * 10
					}
				}
			}
		}
		time.Sleep(time.Second)
	}
	if first == 0 {
		t.Fatal("real Grafana scheduler never evaluated parity-live")
	}
	last := time.Now().UTC().Unix()/10*10 - 10
	length := time.Duration(last-first) * time.Second
	dir := alertProject(t, length.String(), "10s", []map[string]any{rule, record})
	recordedFile := filepath.Join(t.TempDir(), "recorded.json")
	runs := obsAlertSteps(t, binary, dir, first, "--recorded", recordedFile)
	var annotations []struct {
		AlertName, PrevState, NewState string
		Time                           int64
	}
	body := get(t, fmt.Sprintf("%s/api/annotations?type=alert&limit=1000&from=%d&to=%d", os.Getenv("OBS_PARITY_GRAFANA"), created.Add(-time.Minute).UnixMilli(), (last+1)*1000))
	if err := json.Unmarshal(body, &annotations); err != nil {
		t.Fatal(err)
	}
	parityEvidence(t, "grafana-annotations", json.RawMessage(body))
	var real, ours []string
	for _, a := range annotations {
		if a.AlertName == "parity live" && a.Time <= last*1000 {
			real = append(real, fmt.Sprintf("%d|%s|%s", a.Time, a.PrevState, a.NewState))
		}
	}
	for _, run := range runs {
		if run.Alert == "parity-live" {
			for _, tr := range run.Transitions {
				if tr.T > last*1000 || tr.From == tr.To {
					continue
				}
				ours = append(ours, fmt.Sprintf("%d|%s|%s", tr.T, tr.From, tr.To))
			}
		}
	}
	sort.Strings(real)
	sort.Strings(ours)
	if len(real) < 4 || !reflect.DeepEqual(ours, real) {
		t.Fatalf("alerts/parity/warning.yaml rule parity-live state history: obs %v, Grafana %v (annotations %s)", ours, real, body)
	}
	t.Logf("MEASURED %d real Grafana scheduler transitions equal obs, including pending and keep_firing_for", len(real))
	compareRecorded(t, recordedFile, metric, (max(first, firstRecord)+10)*1000, last*1000)
}

func compareRecorded(t *testing.T, file, metric string, first, last int64) {
	t.Helper()
	type series struct {
		Labels     map[string]string `json:"labels"`
		Metric     map[string]string `json:"metric"`
		Timestamps []int64           `json:"timestamps"`
		Values     []float64         `json:"values"`
	}
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var ours []series
	if err := json.Unmarshal(b, &ours); err != nil {
		t.Fatal(err)
	}
	parityEvidence(t, "obs-recorded", json.RawMessage(b))
	params := url.Values{"match[]": {metric}, "start": {strconv.FormatInt(first/1000, 10)}, "end": {strconv.FormatInt(last/1000, 10)}}
	response := get(t, os.Getenv("OBS_PARITY_VM")+"/api/v1/export?"+params.Encode())
	var real []series
	scanner := bufio.NewScanner(bytes.NewReader(response))
	for scanner.Scan() {
		var row series
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			t.Fatal(err)
		}
		real = append(real, row)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	parityEvidence(t, "grafana-recorded", real)
	points := func(rows []series) map[string][]float64 {
		out := map[string][]float64{}
		for _, row := range rows {
			labels := row.Labels
			if labels == nil {
				labels = row.Metric
			}
			if labels["__name__"] != metric {
				continue
			}
			key, _ := json.Marshal(labels)
			for i, at := range row.Timestamps {
				if at >= first && at <= last {
					out[string(key)+"@"+strconv.FormatInt(at, 10)] = []float64{row.Values[i]}
				}
			}
		}
		return out
	}
	got, want := points(ours), points(real)
	if len(want) < 5 {
		t.Fatalf("Grafana recording rule %s wrote too few points: %v", metric, want)
	}
	if err := compareNumbers(got, want); err != nil {
		t.Fatalf("alerts/parity/warning.yaml recording rule %s: %v", metric, err)
	}
	t.Logf("MEASURED %d live Grafana recording-rule points equal obs at 1e-11 relative", len(want))
}
