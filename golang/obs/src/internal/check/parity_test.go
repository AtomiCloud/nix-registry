//go:build parity

package check

// Local parity run: obs's embedded engines against real VictoriaMetrics and VictoriaLogs.
//
//	docker run -d --name obs-parity-vm -p 127.0.0.1::8428 victoriametrics/victoria-metrics:v1.156.0 \
//	    -retentionPeriod=10y -search.latencyOffset=0s -search.disableCache
//	docker run -d --name obs-parity-vl -p 127.0.0.1::9428 victoriametrics/victoria-logs:v1.54.0 -retentionPeriod=10y
//	OBS_PARITY_VM=http://127.0.0.1:<port> OBS_PARITY_VL=http://127.0.0.1:<port> \
//	    go test -tags parity -run Parity -v ./internal/check
//
// Every fixture's seed is loaded into both, and every query a live run executes runs on
// both engines; the results must be equal (same series, same values; same row count for a
// log query). The fixtures' own verdicts (rows or no rows) must therefore be equal too.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/AtomiCloud/obs/internal/engines/vl"
	"github.com/AtomiCloud/obs/internal/engines/vm"
	"github.com/AtomiCloud/obs/internal/files"
	"github.com/AtomiCloud/obs/internal/seed"
)

type tally struct {
	compared      int
	empty         int
	mismatch      []string
	byFixture     map[string]int
	grafanaPanels int
}

func labelKey(ls []seed.Label) string {
	c := append([]seed.Label{}, ls...)
	sort.Slice(c, func(i, j int) bool { return c[i].Name < c[j].Name })
	var b strings.Builder
	for _, l := range c {
		b.WriteString(l.Name + "=" + l.Value + ",")
	}
	return b.String()
}

func near(a, b float64) bool {
	if a == b {
		return true
	}
	return math.Abs(a-b) <= 1e-11*math.Max(math.Abs(a), math.Abs(b))
}

func get(t *testing.T, u string) []byte {
	t.Helper()
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode/100 != 2 {
		return []byte(fmt.Sprintf(`{"status":"error","error":%q}`, strings.TrimSpace(string(b))))
	}
	return b
}

type teeVM struct {
	t    *testing.T
	ours *vm.Engine
	base string
	ta   *tally
	fx   string
}

type promResp struct {
	Status string `json:"status"`
	Error  string `json:"error"`
	Data   struct {
		Result []struct {
			Metric map[string]string `json:"metric"`
			Value  []any             `json:"value"`
			Values [][]any           `json:"values"`
		} `json:"result"`
	} `json:"data"`
}

func (e *teeVM) record(what string, ours []vm.Series, real promResp, instant bool) {
	e.ta.compared++
	e.ta.byFixture[e.fx]++
	if len(ours) == 0 {
		e.ta.empty++
	}
	if real.Status != "success" {
		e.ta.mismatch = append(e.ta.mismatch, fmt.Sprintf("%s: real VictoriaMetrics: %s %s", what, real.Status, real.Error))
		return
	}
	want := map[string][]float64{}
	for _, r := range real.Data.Result {
		var ls []seed.Label
		for k, v := range r.Metric {
			ls = append(ls, seed.Label{Name: k, Value: v})
		}
		var vals []float64
		if instant {
			if len(r.Value) == 2 {
				f, _ := strconv.ParseFloat(r.Value[1].(string), 64)
				vals = append(vals, f)
			}
		} else {
			for _, p := range r.Values {
				f, _ := strconv.ParseFloat(p[1].(string), 64)
				if !math.IsNaN(f) {
					vals = append(vals, f)
				}
			}
		}
		if len(vals) > 0 {
			want[labelKey(ls)] = vals
		}
	}
	got := map[string][]float64{}
	for _, s := range ours {
		got[labelKey(s.Labels)] = s.Values
	}
	if len(got) != len(want) {
		e.ta.mismatch = append(e.ta.mismatch, fmt.Sprintf("%s: obs %d series, real %d series", what, len(got), len(want)))
		return
	}
	for k, gv := range got {
		wv, ok := want[k]
		if !ok || len(wv) != len(gv) {
			e.ta.mismatch = append(e.ta.mismatch, fmt.Sprintf("%s: series {%s}: obs %d points, real %d (present=%v)", what, k, len(gv), len(wv), ok))
			return
		}
		for i := range gv {
			if !near(gv[i], wv[i]) {
				e.ta.mismatch = append(e.ta.mismatch, fmt.Sprintf("%s: series {%s} point %d: obs %v, real %v", what, k, i, gv[i], wv[i]))
				return
			}
		}
	}
}

func (e *teeVM) Instant(q string, at time.Time) ([]vm.Series, error) {
	ours, err := e.ours.Instant(q, at)
	if err != nil {
		return nil, err
	}
	var real promResp
	_ = json.Unmarshal(get(e.t, e.base+"/api/v1/query?"+url.Values{"query": {q}, "time": {strconv.FormatInt(at.Unix(), 10)}, "nocache": {"1"}}.Encode()), &real)
	e.record(fmt.Sprintf("instant %s", q), ours, real, true)
	return ours, nil
}

func (e *teeVM) Range(q string, start, end time.Time, step time.Duration) ([]vm.Series, error) {
	ours, err := e.ours.Range(q, start, end, step)
	if err != nil {
		return nil, err
	}
	var real promResp
	v := url.Values{"query": {q}, "start": {strconv.FormatInt(start.Unix(), 10)}, "end": {strconv.FormatInt(end.Unix(), 10)}, "step": {strconv.FormatInt(int64(step/time.Second), 10)}, "nocache": {"1"}}
	_ = json.Unmarshal(get(e.t, e.base+"/api/v1/query_range?"+v.Encode()), &real)
	e.record(fmt.Sprintf("range %s step %s", q, step), ours, real, false)
	if os.Getenv("OBS_PARITY_GRAFANA") != "" {
		frames := grafanaQuery(e.t, map[string]any{"datasource": map[string]string{"uid": "parity-metrics", "type": "prometheus"}, "expr": q, "range": true, "instant": false, "intervalMs": step.Milliseconds(), "maxDataPoints": 100000}, start, end)
		got := map[string][]float64{}
		for _, series := range ours {
			var labels []seed.Label
			for _, label := range series.Labels {
				if label.Name != "__name__" {
					labels = append(labels, label)
				}
			}
			got[labelKey(labels)] = series.Values
		}
		if err := compareNumbers(got, frameNumbers(e.t, frames)); err != nil {
			e.ta.mismatch = append(e.ta.mismatch, fmt.Sprintf("Grafana panel fixture %s query %q: %v", e.fx, q, err))
		}
		e.ta.grafanaPanels++
	}
	return ours, nil
}

type teeVL struct {
	t    *testing.T
	ours *vl.Engine
	base string
	ta   *tally
	fx   string
}

func (e *teeVL) Stats(qs string, start, end time.Time, drop bool) ([]vl.Result, error) {
	ours, err := e.ours.Stats(qs, start, end, drop)
	if err != nil {
		return nil, err
	}
	realQ := qs
	if drop {
		if realQ, err = vl.WithoutTrailingFilters(qs, end); err != nil {
			return nil, err
		}
	}
	e.ta.compared++
	e.ta.byFixture[e.fx]++
	if len(ours) == 0 {
		e.ta.empty++
	}
	v := url.Values{"query": {realQ}, "start": {strconv.FormatInt(start.UnixNano(), 10)}, "end": {strconv.FormatInt(end.UnixNano(), 10)}, "time": {strconv.FormatInt(end.UnixNano(), 10)}}
	var real struct {
		Status string `json:"status"`
		Error  string `json:"error"`
		Data   struct {
			Result []struct {
				Metric map[string]string `json:"metric"`
				Value  []any             `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	_ = json.Unmarshal(get(e.t, e.base+"/select/logsql/stats_query?"+v.Encode()), &real)
	what := fmt.Sprintf("stats %s [%s, %s)", qs, start.Format(time.RFC3339), end.Format(time.RFC3339))
	if real.Status != "success" {
		e.ta.mismatch = append(e.ta.mismatch, fmt.Sprintf("%s: real VictoriaLogs: %s %s", what, real.Status, real.Error))
		return ours, nil
	}
	want := map[string]float64{}
	for _, r := range real.Data.Result {
		var ls []seed.Label
		for k, x := range r.Metric {
			if k != "__name__" {
				ls = append(ls, seed.Label{Name: k, Value: x})
			}
		}
		f, _ := strconv.ParseFloat(r.Value[1].(string), 64)
		want[labelKey(ls)] = f
	}
	got := map[string]float64{}
	for _, r := range ours {
		f, _ := r.Float()
		got[labelKey(r.Labels[:len(r.Labels)])] = f
	}
	if len(got) != len(want) {
		e.ta.mismatch = append(e.ta.mismatch, fmt.Sprintf("%s: obs %d rows %v, real %d rows %v", what, len(got), got, len(want), want))
		return ours, nil
	}
	for k, g := range got {
		if w, ok := want[k]; !ok || !near(g, w) {
			e.ta.mismatch = append(e.ta.mismatch, fmt.Sprintf("%s: row {%s}: obs %v, real %v (present=%v)", what, k, g, w, ok))
			return ours, nil
		}
	}
	return ours, nil
}

func (e *teeVL) Rows(qs string, start, end time.Time) ([]vl.Result, error) {
	ours, err := e.ours.Rows(qs, start, end)
	if err != nil {
		return nil, err
	}
	e.ta.compared++
	e.ta.byFixture[e.fx]++
	if len(ours) == 0 {
		e.ta.empty++
	}
	v := url.Values{"query": {qs}, "start": {strconv.FormatInt(start.UnixNano(), 10)}, "end": {strconv.FormatInt(end.UnixNano(), 10)}}
	body := get(e.t, e.base+"/select/logsql/query?"+v.Encode())
	n := 0
	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		if len(bytes.TrimSpace(sc.Bytes())) > 0 {
			n++
		}
	}
	if bytes.HasPrefix(body, []byte(`{"status":"error"`)) {
		e.ta.mismatch = append(e.ta.mismatch, fmt.Sprintf("rows %s: real VictoriaLogs: %s", qs, body))
	} else if n != len(ours) {
		e.ta.mismatch = append(e.ta.mismatch, fmt.Sprintf("rows %s [%s, %s): obs %d rows, real %d rows", qs, start.Format(time.RFC3339), end.Format(time.RFC3339), len(ours), n))
	}
	return ours, nil
}

func loadReal(t *testing.T, vmURL, vlURL string, ds *seed.Dataset) {
	t.Helper()
	var vmBody bytes.Buffer
	enc := json.NewEncoder(&vmBody)
	for _, s := range ds.Series {
		m := map[string]string{}
		for _, l := range s.Labels {
			m[l.Name] = l.Value
		}
		if err := enc.Encode(map[string]any{"metric": m, "values": s.Vals, "timestamps": s.Ts}); err != nil {
			t.Fatal(err)
		}
	}
	postReal(t, vmURL+"/api/v1/import", "application/json", &vmBody)
	get(t, vmURL+"/internal/force_flush")
	for _, st := range ds.Streams {
		var names []string
		for _, f := range st.Fields {
			names = append(names, f.Name)
		}
		var body bytes.Buffer
		enc := json.NewEncoder(&body)
		for _, r := range st.Rows {
			row := map[string]string{"_msg": r.Msg, "_time": time.Unix(0, r.TsNs).UTC().Format(time.RFC3339Nano)}
			for _, f := range st.Fields {
				row[f.Name] = f.Value
			}
			for _, f := range r.Fields {
				row[f.Name] = f.Value
			}
			if err := enc.Encode(row); err != nil {
				t.Fatal(err)
			}
		}
		q := url.Values{"_stream_fields": {strings.Join(names, ",")}, "_msg_field": {"_msg"}, "_time_field": {"_time"}}
		postReal(t, vlURL+"/insert/jsonline?"+q.Encode(), "application/stream+json", &body)
	}
	get(t, vlURL+"/internal/force_flush")
	time.Sleep(2 * time.Second)
}

func postReal(t *testing.T, endpoint, contentType string, body io.Reader) []byte {
	t.Helper()
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Post(endpoint, contentType, body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode/100 != 2 {
		t.Fatalf("POST %s: HTTP %d: %s", endpoint, resp.StatusCode, b)
	}
	return b
}

func TestParity(t *testing.T) {
	vmURL, vlURL := os.Getenv("OBS_PARITY_VM"), os.Getenv("OBS_PARITY_VL")
	if vmURL == "" || vlURL == "" {
		t.Skip("OBS_PARITY_VM and OBS_PARITY_VL not set")
	}
	fx := "../../../tests/fixtures"
	ta := &tally{byFixture: map[string]int{}}
	// Each fixture gets a fresh time line, so every fixture loads into the real stores on its own
	// series and rows (different resource labels never collide, same-label fixtures are disjoint in time).
	type run struct{ name, dir, seed string }
	runs := []run{
		{"good", fx + "/good/observability", ""},
		{"charlotte", fx + "/charlotte/observability", ""},
	}
	for _, n := range []string{"bad-no-rows", "bad-drifted-field", "bad-dashboard-label", "ok-rollup-no-window"} {
		runs = append(runs, run{n, filepath.Join(fx, n, "observability"), fx + "/shared-seed.yaml"})
	}
	for i, r := range runs {
		set, err := files.Load(r.dir)
		if err != nil {
			t.Fatal(err)
		}
		// Disjoint time lines: fixture i starts i*6h further back (each seed is at most 4 h).
		o := Options{Dir: r.dir, SeedPath: r.seed, Base: time.Now().Add(-time.Duration(i+1) * 6 * time.Hour).Unix()}
		ds, err := makeData(o)
		if err != nil {
			t.Fatal(err)
		}
		loadReal(t, vmURL, vlURL, ds)
		ours, err := vm.New(ds)
		if err != nil {
			t.Fatal(err)
		}
		lours, err := vl.New(ds)
		if err != nil {
			t.Fatal(err)
		}
		rep := runLive(set, o, ds, &teeVM{t: t, ours: ours, base: vmURL, ta: ta, fx: r.name}, &teeVL{t: t, ours: lours, base: vlURL, ta: ta, fx: r.name})
		ours.Close()
		lours.Close()
		t.Logf("%s: %s", r.name, rep.Summary("live"))
		if strings.HasPrefix(r.name, "bad-") {
			if rep.Failures() == 0 {
				t.Errorf("%s: planted bad fixture unexpectedly passed", r.dir)
			}
		} else if rep.Failures() != 0 {
			t.Errorf("%s: baseline fixture failed: %s", r.dir, rep.Summary("live"))
		}
	}
	t.Logf("compared %d query executions (%d with an empty result on both sides): %v", ta.compared, ta.empty, ta.byFixture)
	t.Logf("compared %d metric panel executions through Grafana /api/ds/query", ta.grafanaPanels)
	for _, m := range ta.mismatch {
		t.Errorf("MISMATCH %s", m)
	}
	if ta.compared == 0 {
		t.Fatal("nothing compared")
	}
}
