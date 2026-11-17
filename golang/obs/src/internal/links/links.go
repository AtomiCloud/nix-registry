// Package links checks cross-signal queries using only the seeded stores.
package links

import (
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/AtomiCloud/obs/internal/engines/tempo"
	"github.com/AtomiCloud/obs/internal/engines/vl"
	"github.com/AtomiCloud/obs/internal/seed"
)

type Finding struct{ Kind, Subject, Query, Error string }
type Report struct {
	Checked  map[string]int
	Findings []Finding
}
type Logs interface {
	Rows(string, time.Time, time.Time) ([]vl.Result, error)
}
type Traces interface {
	Search(string, time.Time, time.Time, int) ([]tempo.TraceHit, error)
}

// TraceToLogs reproduces Grafana 13.3.0's getQueryForVictoriaLogs with
// filterByTraceID=true, filterBySpanID=false, tags=[{key:"service.name"}].
// Explicit unmapped tags are required: Grafana's default maps dots to underscores.
// Source and parity status are documented in README; not UI parity-tested yet.
func TraceToLogs(s *seed.TSpan, id string) (string, time.Time, time.Time) {
	q := fmt.Sprintf("trace_id:=%s AND service.name:=%s", strconv.Quote(id), strconv.Quote(s.Service))
	from := int64(s.StartNs / 1e6)
	to := int64((s.StartNs + s.DurNs) / 1e6)
	if to <= from {
		to = from + 1
	}
	return q, time.UnixMilli(from), time.UnixMilli(to)
}

// ProfileLabels performs the default light check: service and pod identity are
// present and can be mapped to Pyroscope's label spelling. It does not require a
// CPU sample in every span (short spans legitimately have none).
func ProfileLabels(s *seed.TSpan) (map[string]string, error) {
	if s.Service == "" {
		return nil, fmt.Errorf("missing service.name for profile label service_name")
	}
	pod := s.Resource["k8s.pod.name"].S
	if pod == "" {
		return nil, fmt.Errorf("missing k8s.pod.name for profile label pod")
	}
	m := map[string]string{"service_name": s.Service, "pod": pod}
	for _, key := range []string{"service.namespace", "deployment.environment.name", "durian.cloud.landscape", "durian.cloud.platform", "durian.cloud.service", "durian.cloud.module"} {
		if v := s.Resource[key]; v.S != "" {
			name := strings.ReplaceAll(key, ".", "_")
			if key == "deployment.environment.name" {
				name = "deployment_environment"
			}
			m[name] = v.S
		}
	}
	return m, nil
}

type attr struct {
	scope, name string
	kind        int8
}

// metricQuery keeps every series label except the metric name and histogram
// boundary. A label without a corresponding resource/span attribute is an error,
// so dropping a condition can never make a broken link look healthy.
func metricQuery(labels []seed.Label, inventory map[string]attr) (string, error) {
	var parts []string
	for _, l := range labels {
		if l.Name == "__name__" || l.Name == "le" {
			continue
		}
		a, ok := inventory[l.Name]
		if !ok {
			return "", fmt.Errorf("series label %s=%q has no shared trace attribute", l.Name, l.Value)
		}
		value := strconv.Quote(l.Value)
		switch a.kind {
		case 1:
			n, err := strconv.ParseInt(l.Value, 10, 64)
			if err != nil {
				return "", fmt.Errorf("label %s=%q is not an int", l.Name, l.Value)
			}
			value = strconv.FormatInt(n, 10)
		case 2:
			n, err := strconv.ParseFloat(l.Value, 64)
			if err != nil {
				return "", err
			}
			value = strconv.FormatFloat(n, 'f', -1, 64)
			if !strings.Contains(value, ".") {
				value += ".0"
			}
		case 3:
			n, err := strconv.ParseBool(l.Value)
			if err != nil {
				return "", err
			}
			value = strconv.FormatBool(n)
		}
		parts = append(parts, a.scope+"."+a.name+" = "+value)
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("series has no shared labels")
	}
	return "{ " + strings.Join(parts, " && ") + " }", nil
}

func value(labels []seed.Label, name string) string {
	for _, l := range labels {
		if l.Name == name {
			return l.Value
		}
	}
	return ""
}

// Check exercises each server span's trace-to-logs link and profile labels, and
// each metric series' TraceQL link in a seeded minute. Search stays capped at 20;
// it proves existence, not an unbounded enumeration of all traces.
func Check(ds *seed.Dataset, logs Logs, traces Traces) *Report {
	r := &Report{Checked: map[string]int{}}
	add := func(kind, subject, q string, err error) {
		r.Checked[kind]++
		if err != nil {
			r.Findings = append(r.Findings, Finding{kind, subject, q, err.Error()})
		}
	}
	if len(ds.Traces) == 0 {
		add("seed", "traces", "", fmt.Errorf("no traces in seed.traces"))
		return r
	}
	inventory := map[string]attr{}
	seenRoutes := map[string]bool{}
	for _, t := range ds.Traces {
		for _, s := range t.Spans {
			inventory["service_name"] = attr{"resource", "service.name", 0}
			for k, v := range s.Resource {
				inventory[strings.ReplaceAll(k, ".", "_")] = attr{"resource", k, v.Kind}
			}
			for k, v := range s.Attrs {
				inventory[strings.ReplaceAll(k, ".", "_")] = attr{"span", k, v.Kind}
			}
			if s.Kind != 2 {
				continue
			}
			route := fmt.Sprint(s.Name, s.Service, s.Resource, s.Attrs)
			if seenRoutes[route] {
				continue
			}
			seenRoutes[route] = true
			id := hex.EncodeToString(t.TraceID[:])
			subject := fmt.Sprintf("route %q trace_id=%s span_id=%x", s.Name, id, s.SpanID)
			q, from, to := TraceToLogs(s, id)
			rows, err := logs.Rows(q, from, to)
			if err == nil {
				err = checkRows(ds, s, id, from, to, rows)
			}
			add("trace-to-logs", subject, q, err)
			_, err = ProfileLabels(s)
			add("span-to-profile", subject, "", err)
		}
	}
	keys := make([]string, 0, len(ds.Series))
	for key := range ds.Series {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		s := ds.Series[key]
		q, err := metricQuery(s.Labels, inventory)
		if err == nil {
			// Choose the last minute with activity in this series, using metric
			// samples alone (not trace availability) to select the clicked minute.
			end := ds.End
			for i := len(s.Ts) - 1; i > 0; i-- {
				if s.Vals[i] > s.Vals[i-1] {
					end = time.UnixMilli(s.Ts[i])
					break
				}
			}
			start := end.Add(-time.Minute)
			var hits []tempo.TraceHit
			hits, err = traces.Search(q, start, end, 20)
			if err == nil && len(hits) == 0 {
				err = fmt.Errorf("TraceQL returned no traces for series %v in [%s,%s]; example seeded span attributes=%v", s.Labels, start.Format(time.RFC3339), end.Format(time.RFC3339), ds.Traces[0].Spans[0].Attrs)
			}
		}
		add("metric-to-traces", fmt.Sprintf("series %v", s.Labels), q, err)
	}
	return r
}

func checkRows(ds *seed.Dataset, s *seed.TSpan, id string, from, to time.Time, rows []vl.Result) error {
	expected := map[string]int{}
	for _, st := range ds.Streams {
		for _, row := range st.Rows {
			service := value(st.Fields, "service.name")
			if service == "" {
				service = value(row.Fields, "service.name")
			}
			if value(row.Fields, "trace_id") == id && service == s.Service && row.TsNs >= from.UnixNano() && row.TsNs < to.UnixNano() {
				expected[fmt.Sprintf("%d %s", row.TsNs, row.Msg)]++
			}
		}
	}
	if len(expected) == 0 {
		return fmt.Errorf("no seeded log lines for trace_id=%s service.name=%q in span range", id, s.Service)
	}
	for _, row := range rows {
		if value(row.Labels, "trace_id") != id || value(row.Labels, "service.name") != s.Service {
			return fmt.Errorf("returned another trace/service's log line for trace_id=%s", id)
		}
		at, err := time.Parse(time.RFC3339Nano, value(row.Labels, "_time"))
		if err != nil {
			return err
		}
		key := fmt.Sprintf("%d %s", at.UnixNano(), value(row.Labels, "_msg"))
		expected[key]--
	}
	for key, n := range expected {
		if n != 0 {
			return fmt.Errorf("trace_id=%s log line %q differs by %d row(s)", id, key, n)
		}
	}
	return nil
}
