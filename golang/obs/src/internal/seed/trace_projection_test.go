package seed

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestMetricProjectionKeepsTraceAndLogIdentity(t *testing.T) {
	metric := MetricDef{Type: "counter", Series: []SeriesDef{{Where: map[string]string{}, Share: 1}}}
	s := Seed{Length: "3s", RandomSeed: 7, Resource: map[string]string{"durian.cloud.service": "fixture"}, Traffic: Traffic{Requests: "3/s"}, Metrics: map[string]MetricDef{"used": metric, "unused": metric}, Logs: []LogDef{{Event: "fixture.used", From: LogSource{Metric: "used"}}}}
	if err := s.Traces.Encode([]TraceDef{
		{From: LogSource{Metric: "used"}, Server: "kept", Client: "child", Attributes: map[string]any{"code": 200, "cached": true}},
		{From: LogSource{Metric: "unused"}, Server: "dropped"},
	}); err != nil {
		t.Fatal(err)
	}
	at := time.Unix(1700000000, 0)
	full, err := Build(&s, at)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := s.WithMetrics(map[string]MetricDef{"used": metric})
	if err != nil {
		t.Fatal(err)
	}
	part, err := Build(&selected, at)
	if err != nil {
		t.Fatal(err)
	}
	var want []*TTrace
	for _, tr := range full.Traces {
		if tr.Spans[0].Name == "kept" {
			want = append(want, tr)
		}
	}
	if len(want) == 0 || !reflect.DeepEqual(want, part.Traces) {
		t.Fatal("retained trace IDs, spans or typed attributes changed")
	}
	if !reflect.DeepEqual(full.Streams, part.Streams) {
		t.Fatal("retained log trace/span IDs changed")
	}
	for k, sr := range part.Series {
		if !reflect.DeepEqual(sr, full.Series[k]) {
			t.Fatal("retained metric samples changed")
		}
	}
	defs, err := s.traceDefs()
	if err != nil || len(defs) != 2 || len(s.Metrics) != 2 {
		t.Fatal("projection mutated the original seed", err)
	}
	withoutLogs := s
	withoutLogs.Logs = nil
	empty, err := withoutLogs.WithMetrics(map[string]MetricDef{})
	if err != nil {
		t.Fatal(err)
	}
	ds, err := Build(&empty, at)
	if err != nil || len(ds.Traces) != 0 || len(ds.Series) != 0 {
		t.Fatal("unused trace sources survived empty metric projection", err)
	}
}

func TestMetricProjectionValidatesOriginalTraceSources(t *testing.T) {
	for _, tc := range []struct {
		name string
		def  TraceDef
		want string
	}{
		{"unknown metric", TraceDef{From: LogSource{Metric: "missing"}, Server: "fixture"}, "missing"},
		{"missing server", TraceDef{From: LogSource{Metric: "unused"}}, "server is required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := Seed{Metrics: map[string]MetricDef{"unused": {Type: "counter"}}}
			if err := s.Traces.Encode([]TraceDef{tc.def}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.WithMetrics(map[string]MetricDef{}); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("bad trace was hidden by metric projection: %v", err)
			}
		})
	}
}
