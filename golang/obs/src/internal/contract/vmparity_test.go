package contract

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	_ "unsafe" // go:linkname to the converter unit table

	"github.com/VictoriaMetrics/VictoriaMetrics/lib/prompb"
	"github.com/VictoriaMetrics/VictoriaMetrics/lib/protoparser/opentelemetry/pb"
	"github.com/VictoriaMetrics/VictoriaMetrics/lib/protoparser/opentelemetry/stream"

	"github.com/AtomiCloud/obs/internal/testfix"
)

// MEASURED parity: production stores OTLP metrics through vminsert with
// -opentelemetry.convertMetricNamesToPrometheus (metric names only; label names keep their dots; NOT
// -opentelemetry.usePrometheusNaming, which also rewrites label names). This feeds real OTLP protobuf
// through VictoriaMetrics' own converter (v1.156.0, lib/protoparser/opentelemetry/stream) with that flag
// and asserts the series names it stores equal StoredNames for every metric kind and every unit the
// contract allows, over the names of the fixture contract and names built to trip a hand-written rule.

func storedByVM(t *testing.T, name, unit, kind string) []string {
	t.Helper()
	if err := flag.Set("opentelemetry.convertMetricNamesToPrometheus", "true"); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = flag.Set("opentelemetry.convertMetricNamesToPrometheus", "false") }()
	one := int64(1)
	sum := 3.0
	m := &pb.Metric{Name: name, Unit: unit}
	switch kind {
	case MetricCounter:
		m.Sum = &pb.Sum{IsMonotonic: true, DataPoints: []*pb.NumberDataPoint{{IntValue: &one, TimeUnixNano: 15e9}}}
	case MetricGauge:
		m.Gauge = &pb.Gauge{DataPoints: []*pb.NumberDataPoint{{IntValue: &one, TimeUnixNano: 15e9}}}
	case MetricHistogram:
		m.Histogram = &pb.Histogram{DataPoints: []*pb.HistogramDataPoint{{Count: 2, Sum: &sum, ExplicitBounds: []float64{0.5}, BucketCounts: []uint64{1, 1}, TimeUnixNano: 15e9}}}
	}
	req := &pb.MetricsData{ResourceMetrics: []*pb.ResourceMetrics{{ScopeMetrics: []*pb.ScopeMetrics{{Metrics: []*pb.Metric{m}}}}}}
	set := map[string]bool{}
	err := stream.ParseStream(bytes.NewBuffer(req.MarshalProtobuf(nil)), "", nil, func(tss []prompb.TimeSeries, _ []prompb.MetricMetadata) error {
		for _, ts := range tss {
			for _, l := range ts.Labels {
				if l.Name == "__name__" {
					set[l.Value] = true
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func TestStoredNamesEqualVictoriaMetricsConversion(t *testing.T) {
	names := []string{
		"a.b", "a_b", "ab",
		"requests", "requests_total", "requests.total", "total", "a.total.b", "a_total_b", "total.requests",
		"x.seconds", "x.seconds.latency", "seconds.x", "x_bytes", "x.bytes.sent", "bytes", "seconds",
		"x.bytes.seconds", "x.total.seconds", "x.seconds.total", "x.bytes.total",
		"a__b", "a._b", "_a", "a_", "a..b",
	}
	if c, err := Load(testfix.GoodContract(t)); err == nil {
		for _, n := range c.MetricOrder {
			names = append(names, n)
		}
	} else {
		t.Fatal(err)
	}
	checked := 0
	for _, name := range names {
		for _, unit := range Units {
			for _, kind := range []string{MetricCounter, MetricGauge, MetricHistogram} {
				want := storedByVM(t, name, unit, kind)
				got := append([]string(nil), StoredNames(name, Metric{Kind: kind, Unit: unit})...)
				sort.Strings(got)
				if strings.Join(got, ",") != strings.Join(want, ",") {
					t.Errorf("%s %q unit %q: obs StoredNames = %v, VictoriaMetrics stores %v", kind, name, unit, got, want)
				}
				checked++
			}
		}
	}
	if checked < 300 {
		t.Fatalf("only %d combinations checked", checked)
	}
	t.Log(fmt.Sprintf("%d (name, unit, kind) combinations equal VictoriaMetrics' conversion", checked))
}

// MEASURED platform parity: a platform metric's kind and unit are not in the contract, so the keep
// list holds a superset (platformStored). Every name VictoriaMetrics' own converter stores for a
// platform metric, whatever its kind (counter, gauge, histogram) and its OTLP unit (s, ms, By, 1),
// must be in that superset, or the collector would drop data the service sends. Names include ones
// that already hold a unit word (io.bytes.sent with By: VictoriaMetrics adds no second _bytes) and
// ones that hold the token it moves (total, ratio) in the middle.
func TestPlatformKeepCoversEveryVictoriaMetricsName(t *testing.T) {
	names := []string{
		"http.server.request.duration", "http.server.duration", "http.client.request.duration",
		"io.bytes.sent", "io.bytes.received", "process.cpu.utilization", "process.runtime.memory",
		"process.cpu.time", "http.server.active_requests", "db.client.connection.count",
		"gc.seconds.pause", "cpu.milliseconds.used", "queue.ratio.depth", "jobs.total.done", "a_b", "a.b",
		"requests_total", "requests.total", "x.seconds.total",
	}
	units := []string{"s", "ms", "By", "1"}
	checked, missed := 0, 0
	for _, name := range names {
		keep := platformStored(name)
		for _, unit := range units {
			if strings.HasSuffix(name, ".duration") && (unit == "By" || unit == "1") {
				continue // by design a duration key is in time: seconds or milliseconds only (platformStored)
			}
			for _, kind := range []string{MetricCounter, MetricGauge, MetricHistogram} {
				for _, stored := range storedByVM(t, name, unit, kind) {
					checked++
					if !in(keep, stored) {
						missed++
						t.Errorf("%s %q unit %q: VictoriaMetrics stores %q, which the platform keep list %v lacks", kind, name, unit, stored, keep)
					}
				}
			}
		}
	}
	if checked < 150 {
		t.Fatalf("only %d stored names checked", checked)
	}
	t.Logf("%d VictoriaMetrics-stored names checked against the platform keep superset, %d missed", checked, missed)
}

// The unit table of VictoriaMetrics v1.156.0 (lib/protoparser/opentelemetry/stream/sanitize.go, unitMap),
// read straight from the pinned package: the table is unexported, so the test links to it by name. The
// production code cannot import the converter (it would pull the ingestion code and its flags into obs),
// so units_gen.go holds the words and TestUnitWordsAreTheVictoriaMetricsTable keeps them equal to the table.
//
//go:linkname vmUnitMap github.com/VictoriaMetrics/VictoriaMetrics/lib/protoparser/opentelemetry/stream.unitMap
var vmUnitMap map[string]string

var updateUnits = flag.Bool("update-units", false, "rewrite units_gen.go from the VictoriaMetrics unit table")

// tableUnits is every OTLP unit the converter knows, sorted.
func tableUnits() []string {
	var out []string
	for u := range vmUnitMap {
		out = append(out, u)
	}
	sort.Strings(out)
	return out
}

func unitsGenSource() string {
	seen := map[string]bool{}
	var words []string
	for _, w := range vmUnitMap {
		if w != "" && !seen[w] {
			seen[w] = true
			words = append(words, w)
		}
	}
	sort.Strings(words)
	var b strings.Builder
	b.WriteString("// Code generated by TestUnitWordsAreTheVictoriaMetricsTable (go test ./internal/contract -run UnitWords -update-units); DO NOT EDIT.\n\n")
	b.WriteString("package contract\n\n")
	b.WriteString("// vmUnitWords are the unit words VictoriaMetrics v1.156.0 appends to a metric name: the values of the unit\n")
	b.WriteString("// table in lib/protoparser/opentelemetry/stream/sanitize.go (the unit 1 maps to no word, so it is not here).\n")
	b.WriteString("var vmUnitWords = []string{\n")
	for _, w := range words {
		fmt.Fprintf(&b, "\t%q,\n", w)
	}
	b.WriteString("}\n")
	return b.String()
}

func TestUnitWordsAreTheVictoriaMetricsTable(t *testing.T) {
	if len(vmUnitMap) < 20 {
		t.Fatalf("the unit table has %d entries: the link to the converter's table is broken", len(vmUnitMap))
	}
	want := unitsGenSource()
	if *updateUnits {
		if err := os.WriteFile("units_gen.go", []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile("units_gen.go")
	if err != nil {
		t.Fatalf("units_gen.go: %v (write it with: go test ./internal/contract -run UnitWords -update-units)", err)
	}
	if string(got) != want {
		t.Fatalf("units_gen.go is not VictoriaMetrics' unit table; run: go test ./internal/contract -run UnitWords -update-units\nwant:\n%s", want)
	}
	for _, w := range vmUnitWords {
		if !in(platformUnits, "_"+w) {
			t.Errorf("platformUnits lacks _%s", w)
		}
	}
}

// MEASURED: every unit of the converter's table (hertz, percent, celsius, ...) and every kind, over
// names that put the moved tokens (total, ratio) and the unit words in every place.
func TestPlatformKeepCoversEveryUnitOfTheTable(t *testing.T) {
	names := []string{
		"system.cpu.frequency", "system.memory.utilization", "process.disk.io", "x.y",
		"requests_total", "requests.total", "http.requests.total", "total.requests", "jobs.total.done",
		"x.seconds.total", "x.bytes.total", "x.total.seconds", "x.hertz", "x.ratio", "queue.ratio.depth",
	}
	checked := 0
	for _, name := range names {
		keep := platformStored(name)
		for _, unit := range tableUnits() {
			for _, kind := range []string{MetricCounter, MetricGauge, MetricHistogram} {
				for _, stored := range storedByVM(t, name, unit, kind) {
					checked++
					if !in(keep, stored) {
						t.Errorf("%s %q unit %q: VictoriaMetrics stores %q, which the platform keep list lacks", kind, name, unit, stored)
					}
				}
			}
		}
	}
	if checked < 1000 {
		t.Fatalf("only %d stored names checked", checked)
	}
	t.Logf("%d stored names over %d units checked against the platform keep superset", checked, len(tableUnits()))
}

// a key ending in _duration keeps only time units, and every one of them is a unit word of the table
func TestDurationUnitsAreTimeWordsOfTheTable(t *testing.T) {
	for _, u := range timeUnitSuffixes {
		if !in(vmUnitWords, strings.TrimPrefix(u, "_")) {
			t.Errorf("%s is not a unit word of VictoriaMetrics' table", u)
		}
	}
	keep := platformStored("http.server.request.duration")
	for _, want := range []string{"http_server_request_duration_seconds_bucket", "http_server_request_duration_nanoseconds_sum", "http_server_request_duration_hours_count", "http_server_request_duration_total"} {
		if !in(keep, want) {
			t.Errorf("no %s", want)
		}
	}
	for _, bad := range []string{"http_server_request_duration_bytes", "http_server_request_duration_ratio", "http_server_request_duration_hertz"} {
		if in(keep, bad) {
			t.Errorf("a duration key lists %s", bad)
		}
	}
	// measured: every time unit of the table, every kind
	checked := 0
	for _, name := range []string{"http.server.request.duration", "queue.wait.duration", "db.duration"} {
		for _, unit := range tableUnits() {
			if !in(timeUnitSuffixes, "_"+vmUnitMap[unit]) {
				continue
			}
			for _, kind := range []string{MetricCounter, MetricGauge, MetricHistogram} {
				for _, stored := range storedByVM(t, name, unit, kind) {
					checked++
					if !in(platformStored(name), stored) {
						t.Errorf("%s %q unit %q: VictoriaMetrics stores %q, which the keep superset lacks", kind, name, unit, stored)
					}
				}
			}
		}
	}
	if checked < 100 {
		t.Fatalf("only %d checked", checked)
	}
}
