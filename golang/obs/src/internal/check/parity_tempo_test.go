//go:build parity

package check

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/AtomiCloud/obs/internal/engines/tempo"
	"github.com/AtomiCloud/obs/internal/engines/vl"
	"github.com/AtomiCloud/obs/internal/links"
	"github.com/AtomiCloud/obs/internal/seed"
)

func traceDataset(t *testing.T) *seed.Dataset {
	t.Helper()
	dir := t.TempDir()
	source := `length: 2m
random_seed: 42
resource:
  service.name: parity-trace
  durian.cloud.service: parity-trace
  durian.cloud.module: api
  k8s.pod.name: parity-0
traffic: {requests: 6/m}
metrics:
  parity.requests:
    type: counter
    series:
      - {where: {outcome: ok}, share: 0.5}
      - {where: {outcome: error}, share: 0.5}
logs:
  - {event: parity.ok, level: info, from: {metric: parity.requests, where: {outcome: ok}}}
  - {event: parity.error, level: error, from: {metric: parity.requests, where: {outcome: error}}}
traces:
  - from: {metric: parity.requests}
    server: POST /parity
    route: /parity
    client: db.query
    median_s: 0.08
    error_when: {outcome: error}
    attributes: {shard: 3, cached: true}
`
	file := filepath.Join(dir, "seed.yaml")
	if err := os.WriteFile(file, []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	sd, err := seed.Load(file)
	if err != nil {
		t.Fatal(err)
	}
	ds, err := seed.Build(sd, time.Now().UTC().Truncate(time.Minute).Add(-10*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(ds.Traces) == 0 {
		t.Fatal("no seeded traces")
	}
	return ds
}

func otlpAttributes(attrs map[string]seed.TAttrVal) []any {
	var out []any
	for key, value := range attrs {
		var val map[string]any
		switch value.Kind {
		case 0:
			val = map[string]any{"stringValue": value.S}
		case 1:
			val = map[string]any{"intValue": strconv.FormatInt(value.I, 10)}
		case 2:
			val = map[string]any{"doubleValue": value.F}
		case 3:
			val = map[string]any{"boolValue": value.B}
		}
		out = append(out, map[string]any{"key": key, "value": val})
	}
	return out
}

func loadTempo(t *testing.T, ds *seed.Dataset) {
	t.Helper()
	if os.Getenv("OBS_PARITY_OTLP") == "" || os.Getenv("OBS_PARITY_TEMPO") == "" {
		t.Skip("OBS_PARITY_OTLP and OBS_PARITY_TEMPO not set")
	}
	var resources []any
	for _, trace := range ds.Traces {
		for _, span := range trace.Spans {
			attrs := map[string]seed.TAttrVal{"service.name": seed.TStr(span.Service)}
			for key, value := range span.Resource {
				attrs[key] = value
			}
			model := map[string]any{"traceId": hex.EncodeToString(trace.TraceID[:]), "spanId": hex.EncodeToString(span.SpanID[:]), "name": span.Name, "kind": span.Kind, "startTimeUnixNano": strconv.FormatUint(span.StartNs, 10), "endTimeUnixNano": strconv.FormatUint(span.StartNs+span.DurNs, 10), "attributes": otlpAttributes(span.Attrs), "status": map[string]int{"code": span.Status}}
			if span.ParentID != ([8]byte{}) {
				model["parentSpanId"] = hex.EncodeToString(span.ParentID[:])
			}
			resources = append(resources, map[string]any{"resource": map[string]any{"attributes": otlpAttributes(attrs)}, "scopeSpans": []any{map[string]any{"scope": map[string]string{"name": "obs-parity"}, "spans": []any{model}}}})
		}
	}
	body, _ := json.Marshal(map[string]any{"resourceSpans": resources})
	response := postReal(t, os.Getenv("OBS_PARITY_OTLP")+"/v1/traces", "application/json", bytes.NewReader(body))
	var result struct {
		PartialSuccess struct {
			RejectedSpans string `json:"rejectedSpans"`
			ErrorMessage  string `json:"errorMessage"`
		} `json:"partialSuccess"`
	}
	if err := json.Unmarshal(response, &result); err != nil {
		t.Fatal(err)
	}
	if result.PartialSuccess.ErrorMessage != "" || (result.PartialSuccess.RejectedSpans != "" && result.PartialSuccess.RejectedSpans != "0") {
		t.Fatalf("Tempo rejected seed spans: %s", response)
	}
	for attempt := 0; attempt < 30; attempt++ {
		hits, err := realTempoSearch(t, `{resource.service.name="parity-trace"}`, ds.Start, ds.End, 1000)
		if err == nil && len(hits) == len(ds.Traces) {
			return
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("Tempo did not make all %d seeded traces searchable", len(ds.Traces))
}

func realTempoSearch(t *testing.T, query string, start, end time.Time, limit int) ([]tempo.TraceHit, error) {
	t.Helper()
	params := url.Values{"q": {query}, "start": {fmt.Sprint(start.Unix())}, "end": {fmt.Sprint(end.Unix())}, "limit": {fmt.Sprint(limit)}, "spss": {"100"}}
	body := get(t, os.Getenv("OBS_PARITY_TEMPO")+"/api/search?"+params.Encode())
	var result struct {
		Error  string `json:"error"`
		Traces []struct {
			TraceID string `json:"traceID"`
			SpanSet struct {
				Spans []struct {
					SpanID string `json:"spanID"`
				} `json:"spans"`
			} `json:"spanSet"`
			SpanSets []struct {
				Spans []struct {
					SpanID string `json:"spanID"`
				} `json:"spans"`
			} `json:"spanSets"`
		} `json:"traces"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	if result.Error != "" {
		return nil, fmt.Errorf("Tempo query %q: %s", query, result.Error)
	}
	var out []tempo.TraceHit
	pad := func(s string, n int) string { return strings.Repeat("0", max(0, n-len(s))) + strings.ToLower(s) }
	for _, trace := range result.Traces {
		seen := map[string]bool{}
		for _, span := range trace.SpanSet.Spans {
			seen[pad(span.SpanID, 16)] = true
		}
		for _, set := range trace.SpanSets {
			for _, span := range set.Spans {
				seen[pad(span.SpanID, 16)] = true
			}
		}
		hit := tempo.TraceHit{TraceID: pad(trace.TraceID, 32)}
		for id := range seen {
			hit.SpanIDs = append(hit.SpanIDs, id)
		}
		sort.Strings(hit.SpanIDs)
		out = append(out, hit)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TraceID < out[j].TraceID })
	return out, nil
}

func TestParityTraceQL(t *testing.T) {
	ds := traceDataset(t)
	loadTempo(t, ds)
	engine := tempo.NewTempoEngine(ds.Traces)
	queries := []string{`{resource.service.name="parity-trace"}`, `{status=error}`, `{span.shard=3 && span.cached=true}`, `{name="POST /parity"} >> {name="db.query"}`, `{name="db.query"} << {name="POST /parity"}`, `{duration > 1ms}`, `{name="missing"}`}
	for _, query := range queries {
		got, err := engine.Search(query, ds.Start, ds.End, 1000)
		if err != nil {
			t.Fatal(err)
		}
		want, err := realTempoSearch(t, query, ds.Start, ds.End, 1000)
		if err != nil {
			t.Fatal(err)
		}
		// Ignore nil versus empty slices, but compare every trace and matching span.
		keys := func(hits []tempo.TraceHit) []string {
			var out []string
			for _, h := range hits {
				sort.Strings(h.SpanIDs)
				out = append(out, h.TraceID+":"+strings.Join(h.SpanIDs, ","))
			}
			sort.Strings(out)
			return out
		}
		if !reflect.DeepEqual(keys(got), keys(want)) {
			t.Fatalf("seed.yaml TraceQL %q: obs %v, Tempo %v", query, keys(got), keys(want))
		}
	}
	metricQueries := []string{`{} | count_over_time()`, `{kind=server} | count_over_time()`}
	for _, query := range metricQueries {
		step := time.Minute
		got, err := engine.MetricsRange(query, ds.Start, ds.End, step)
		if err != nil {
			t.Fatal(err)
		}
		params := url.Values{"q": {query}, "start": {fmt.Sprint(ds.Start.Unix())}, "end": {fmt.Sprint(ds.End.Unix())}, "step": {step.String()}, "exemplars": {"0"}}
		body := get(t, os.Getenv("OBS_PARITY_TEMPO")+"/api/metrics/query_range?"+params.Encode())
		var response struct {
			Error  string `json:"error"`
			Series []struct {
				Labels []struct {
					Key   string                     `json:"key"`
					Value map[string]json.RawMessage `json:"value"`
				} `json:"labels"`
				Samples []struct {
					TimestampMs json.RawMessage `json:"timestampMs"`
					Value       json.RawMessage `json:"value"`
				} `json:"samples"`
			} `json:"series"`
		}
		if err := json.Unmarshal(body, &response); err != nil {
			t.Fatalf("Tempo metrics %q: %v: %s", query, err, body)
		}
		if response.Error != "" {
			t.Fatalf("Tempo metrics %q: %s", query, body)
		}
		number := func(raw json.RawMessage) float64 {
			// Protobuf JSON may omit a scalar whose value is zero.
			if len(raw) == 0 {
				return 0
			}
			if string(raw) == "null" {
				return math.NaN()
			}
			n, err := strconv.ParseFloat(strings.Trim(string(raw), `"`), 64)
			if err != nil {
				t.Fatalf("Tempo metrics %q invalid number %s", query, raw)
			}
			return n
		}
		wantNumbers, gotNumbers := map[string][]float64{}, map[string][]float64{}
		for _, series := range response.Series {
			var labels []seed.Label
			for _, label := range series.Labels {
				for kind, value := range label.Value {
					var text string
					if kind == "stringValue" {
						if err := json.Unmarshal(value, &text); err != nil {
							t.Fatal(err)
						}
					} else {
						text = strings.Trim(string(value), `"`)
					}
					labels = append(labels, seed.Label{Name: label.Key, Value: text})
				}
			}
			for _, sample := range series.Samples {
				value := number(sample.Value)
				if !math.IsNaN(value) {
					wantNumbers[labelKey(labels)+"@"+fmt.Sprint(int64(number(sample.TimestampMs)))] = []float64{value}
				}
			}
		}
		for _, series := range got {
			for at, value := range series.Points {
				if !math.IsNaN(value) {
					gotNumbers[labelKey(series.Labels)+"@"+fmt.Sprint(at)] = []float64{value}
				}
			}
		}
		if len(wantNumbers) == 0 {
			t.Fatalf("Tempo metrics %q returned no seed points: %s", query, body)
		}
		if err := compareNumbers(gotNumbers, wantNumbers); err != nil {
			t.Fatalf("seed.yaml TraceQL metrics %q: %v", query, err)
		}
	}
	t.Logf("MEASURED %d TraceQL searches and %d range-metric queries, including structural and typed-attribute queries, across %d seeded traces", len(queries), len(metricQueries), len(ds.Traces))
}

type realTraceLinks struct{ t *testing.T }

func (r realTraceLinks) Search(query string, start, end time.Time, limit int) ([]tempo.TraceHit, error) {
	return realTempoSearch(r.t, query, start, end, limit)
}

type realLogLinks struct{ t *testing.T }

func (r realLogLinks) Rows(query string, start, end time.Time) ([]vl.Result, error) {
	params := url.Values{"query": {query}, "start": {fmt.Sprint(start.UnixNano())}, "end": {fmt.Sprint(end.UnixNano())}}
	body := get(r.t, os.Getenv("OBS_PARITY_VL")+"/select/logsql/query?"+params.Encode())
	var out []vl.Result
	scanner := bufio.NewScanner(bytes.NewReader(body))
	for scanner.Scan() {
		var row map[string]string
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			return nil, err
		}
		result := vl.Result{Value: row["_msg"]}
		for key, value := range row {
			result.Labels = append(result.Labels, seed.Label{Name: key, Value: value})
		}
		out = append(out, result)
	}
	return out, scanner.Err()
}

func TestParityLinks(t *testing.T) {
	ds := traceDataset(t)
	loadTempo(t, ds)
	loadReal(t, os.Getenv("OBS_PARITY_VM"), os.Getenv("OBS_PARITY_VL"), ds)
	logEngine, err := vl.New(ds)
	if err != nil {
		t.Fatal(err)
	}
	defer logEngine.Close()
	embedded := links.Check(ds, logEngine, tempo.NewTempoEngine(ds.Traces))
	real := links.Check(ds, realLogLinks{t}, realTraceLinks{t})
	if len(embedded.Findings) != 0 || len(real.Findings) != 0 || !reflect.DeepEqual(embedded.Checked, real.Checked) {
		t.Fatalf("seed.yaml link queries: obs findings %v, real findings %v; counts obs=%v real=%v", embedded.Findings, real.Findings, embedded.Checked, real.Checked)
	}
	t.Logf("MEASURED link queries against real VictoriaLogs and Tempo equal embedded engines: %v", real.Checked)
}
