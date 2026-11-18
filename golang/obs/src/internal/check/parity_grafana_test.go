//go:build parity

package check

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/AtomiCloud/obs/internal/engines/vl"
	"github.com/AtomiCloud/obs/internal/engines/vm"
	"github.com/AtomiCloud/obs/internal/files"
	"github.com/AtomiCloud/obs/internal/seed"
)

type grafanaFrame struct {
	Schema struct {
		Fields []struct {
			Name   string            `json:"name"`
			Type   string            `json:"type"`
			Labels map[string]string `json:"labels"`
		} `json:"fields"`
	} `json:"schema"`
	Data struct {
		Values [][]any `json:"values"`
	} `json:"data"`
}

func grafanaQuery(t *testing.T, q map[string]any, start, end time.Time) []grafanaFrame {
	t.Helper()
	q["refId"] = "A"
	b, err := json.Marshal(map[string]any{"from": strconv.FormatInt(start.UnixMilli(), 10), "to": strconv.FormatInt(end.UnixMilli(), 10), "queries": []any{q}})
	if err != nil {
		t.Fatal(err)
	}
	body := postReal(t, os.Getenv("OBS_PARITY_GRAFANA")+"/api/ds/query", "application/json", bytes.NewReader(b))
	var result struct {
		Results map[string]struct {
			Error  string         `json:"error"`
			Frames []grafanaFrame `json:"frames"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("Grafana response: %v: %s", err, body)
	}
	r, ok := result.Results["A"]
	if !ok || r.Error != "" {
		t.Fatalf("Grafana query %v: %s", q, body)
	}
	return r.Frames
}

func frameNumbers(t *testing.T, frames []grafanaFrame) map[string][]float64 {
	t.Helper()
	out := map[string][]float64{}
	for _, frame := range frames {
		for i, field := range frame.Schema.Fields {
			if field.Type != "number" {
				continue
			}
			var labels []seed.Label
			for k, v := range field.Labels {
				if k != "__name__" {
					labels = append(labels, seed.Label{Name: k, Value: v})
				}
			}
			key := labelKey(labels)
			if _, ok := out[key]; ok {
				t.Fatalf("duplicate numeric frame labels %s", key)
			}
			if i >= len(frame.Data.Values) {
				t.Fatal("Grafana field missing values")
			}
			for _, value := range frame.Data.Values[i] {
				if value == nil {
					continue
				}
				n, ok := value.(float64)
				if !ok {
					t.Fatalf("Grafana numeric value %T: %v", value, value)
				}
				out[key] = append(out[key], n)
			}
		}
	}
	return out
}

func compareNumbers(got, want map[string][]float64) error {
	if len(got) != len(want) {
		return fmt.Errorf("obs %d series %v, Grafana %d series %v", len(got), got, len(want), want)
	}
	for key, values := range got {
		expected, ok := want[key]
		if !ok || len(values) != len(expected) {
			return fmt.Errorf("labels {%s}: obs %v, Grafana %v (present=%v)", key, values, expected, ok)
		}
		for i, value := range values {
			if !near(value, expected[i]) {
				return fmt.Errorf("labels {%s} point %d: obs %g, Grafana %g", key, i, value, expected[i])
			}
		}
	}
	return nil
}

func requireGrafana(t *testing.T) {
	t.Helper()
	for _, env := range []string{"OBS_PARITY_VM", "OBS_PARITY_VL", "OBS_PARITY_GRAFANA"} {
		if os.Getenv(env) == "" {
			t.Skip(env + " not set")
		}
	}
}

// TestParityGrafanaLogs exercises the real backend plugin, including stats labels,
// end exclusivity and post-stats thresholds. No proxy directly to VL substitutes
// for Grafana's /api/ds/query in this comparison.
func TestParityGrafanaLogs(t *testing.T) {
	requireGrafana(t)
	o := Options{Dir: "../../../tests/fixtures/good/observability", Base: time.Now().UTC().Truncate(time.Minute).Add(-48 * time.Hour).Unix()}
	ds, err := makeData(o)
	if err != nil {
		t.Fatal(err)
	}
	loadReal(t, os.Getenv("OBS_PARITY_VM"), os.Getenv("OBS_PARITY_VL"), ds)
	e, err := vl.New(ds)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	set, err := files.Load(o.Dir)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, query := range set.Queries {
		if !query.IsAlert || query.Kind != files.Logs {
			continue
		}
		duration := time.Duration(query.RangeSecs) * time.Second
		if duration <= 0 {
			duration = 10 * time.Minute
		}
		for at := ds.Start.Add(duration); !at.After(ds.End); at = at.Add(time.Minute) {
			ours, err := e.Stats(query.Expr, at.Add(-duration), at, false)
			if err != nil {
				t.Fatal(err)
			}
			frames := grafanaQuery(t, map[string]any{"datasource": map[string]string{"uid": "parity-logs", "type": "victoriametrics-logs-datasource"}, "expr": query.Expr, "queryType": "stats", "intervalMs": 60000, "maxDataPoints": 1000}, at.Add(-duration), at)
			got := map[string][]float64{}
			for _, row := range ours {
				value, ok := row.Float()
				if !ok {
					t.Fatalf("%s %s: nonnumeric stats %q", query.File, query.Where, row.Value)
				}
				key := labelKey(row.Labels)
				if _, ok := got[key]; ok {
					t.Fatalf("%s %s: duplicate stats labels %s", query.File, query.Where, key)
				}
				got[key] = []float64{value}
			}
			if err := compareNumbers(got, frameNumbers(t, frames)); err != nil {
				t.Fatalf("%s %s query %q at %s: %v", query.File, query.Where, query.Expr, at.Format(time.RFC3339), err)
			}
			count++
		}
	}
	if count == 0 {
		t.Fatal("no LogsQL alert minutes compared")
	}
	t.Logf("MEASURED %d LogsQL alert minute comparisons through Grafana VictoriaLogs datasource", count)
}

// Historic query values use /api/ds/query, the datasource half of Grafana's eval
// API. Full alert expressions and state are compared separately by alerts parity.
func TestParityHistoricMetrics(t *testing.T) {
	requireGrafana(t)
	o := Options{Dir: "../../../tests/fixtures/good/observability", Base: time.Now().UTC().Truncate(time.Minute).Add(-54 * time.Hour).Unix()}
	ds, err := makeData(o)
	if err != nil {
		t.Fatal(err)
	}
	loadReal(t, os.Getenv("OBS_PARITY_VM"), os.Getenv("OBS_PARITY_VL"), ds)
	e, err := vm.New(ds)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	set, err := files.Load(o.Dir)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, query := range set.Queries {
		if !query.IsAlert || query.Kind != files.Metrics {
			continue
		}
		for at := ds.Start.Add(10 * time.Minute); !at.After(ds.End); at = at.Add(time.Minute) {
			ours, err := e.Instant(query.Expr, at)
			if err != nil {
				t.Fatal(err)
			}
			frames := grafanaQuery(t, map[string]any{"datasource": map[string]string{"uid": "parity-metrics", "type": "prometheus"}, "expr": query.Expr, "instant": true, "range": false, "intervalMs": 1000, "maxDataPoints": 1000}, at.Add(-10*time.Minute), at)
			got := map[string][]float64{}
			for _, row := range ours {
				var labels []seed.Label
				for _, label := range row.Labels {
					if label.Name != "__name__" {
						labels = append(labels, label)
					}
				}
				got[labelKey(labels)] = row.Values
			}
			if err := compareNumbers(got, frameNumbers(t, frames)); err != nil {
				t.Fatalf("%s %s query %q at %s: %v", query.File, query.Where, query.Expr, at.Format(time.RFC3339), err)
			}
			count++
		}
	}
	if count == 0 {
		t.Fatal("no metric alert minutes compared")
	}
	t.Logf("MEASURED %d historic MetricsQL alert minute values through Grafana", count)
}

func TestParityComparator(t *testing.T) {
	if err := compareNumbers(map[string][]float64{"x": {100}}, map[string][]float64{"x": {100 + 1e-10}}); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []map[string][]float64{{"x": {100 + 1e-7}}, {"wrong-label": {100}}, {"x": {100, 100}}, {}} {
		if err := compareNumbers(map[string][]float64{"x": {100}}, bad); err == nil {
			t.Fatalf("planted bad series/value accepted: %v", bad)
		}
	}
	for _, pair := range [][2]float64{
		{100, math.Inf(1)}, {100, math.Inf(-1)}, {math.Inf(1), 100},
		{math.Inf(-1), 100}, {math.Inf(1), math.Inf(-1)}, {math.Inf(-1), math.Inf(1)},
		{100, math.NaN()}, {math.NaN(), 100}, {math.NaN(), math.NaN()},
	} {
		if err := compareNumbers(map[string][]float64{"x": {pair[0]}}, map[string][]float64{"x": {pair[1]}}); err == nil {
			t.Errorf("planted nonfinite mismatch accepted: obs %v, Grafana %v", pair[0], pair[1])
		}
	}
	for _, value := range []float64{0, math.Inf(1), math.Inf(-1)} {
		if !near(value, value) {
			t.Errorf("equal values rejected: %v", value)
		}
	}
}
