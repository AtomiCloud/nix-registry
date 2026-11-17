package profile

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pp "github.com/google/pprof/profile"
)

// stackSpec is one stack of a made-up service, leaf first, with its sample count in the base
// run and the span the samples carry (pprof label span_name).
type stackSpec struct {
	frames []string
	n      int
	span   string
}

var tail = []string{"net/http.HandlerFunc.ServeHTTP", "net/http.(*conn).serve", "runtime.goexit"}

func st(n int, span string, frames ...string) stackSpec {
	return stackSpec{frames: append(append([]string{}, frames...), tail...), n: n, span: span}
}

// baseSpec: ~6,000 samples. Own code is everything under "svc/". With --own '^svc/' the shares
// attributed to own functions are: render.Page 40 %, db.Query 30 %, handlers.Browse 10 %
// (its json.Marshal callee), handlers.Order 5 % (its closure), cache.Get 3.5 %, rare.Thing 1.5 %.
func baseSpec() []stackSpec {
	return []stackSpec{
		st(2400, "GET /browse", "svc/render.Page", "svc/handlers.Browse"),
		st(600, "GET /browse", "encoding/json.Marshal", "svc/handlers.Browse"),
		st(1800, "POST /order", "svc/db.Query", "svc/handlers.Order"),
		st(300, "POST /order", "svc/handlers.Order.func1", "svc/handlers.Order"),
		st(210, "GET /browse", "svc/cache.Get", "svc/handlers.Browse"),
		st(90, "", "svc/rare.Thing", "svc/handlers.Order"),
		{frames: []string{"runtime.mallocgc", "runtime.goexit"}, n: 600},
	}
}

// variant changes the count of every stack whose leaf matches.
func variant(spec []stackSpec, scale map[string]float64) []stackSpec {
	out := make([]stackSpec, len(spec))
	for i, s := range spec {
		out[i] = s
		if f, ok := scale[s.frames[0]]; ok {
			out[i].n = int(float64(s.n)*f + 0.5)
		}
	}
	return out
}

// jitter scales every count by a deterministic factor within +-2 %.
func jitter(spec []stackSpec, seed int64) []stackSpec {
	r := rand.New(rand.NewSource(seed))
	out := make([]stackSpec, len(spec))
	for i, s := range spec {
		out[i] = s
		out[i].n = int(float64(s.n)*(0.98+0.04*r.Float64()) + 0.5)
	}
	return out
}

// pprofBytes writes the spec as a gzip pprof profile with samples/count and cpu/nanoseconds
// (10 ms per sample), like Go's CPU profiler, with the span label on spanned samples.
func pprofBytes(t testing.TB, spec []stackSpec) []byte {
	t.Helper()
	p := &pp.Profile{
		SampleType: []*pp.ValueType{{Type: "samples", Unit: "count"}, {Type: "cpu", Unit: "nanoseconds"}},
		PeriodType: &pp.ValueType{Type: "cpu", Unit: "nanoseconds"},
		Period:     10_000_000,
	}
	fns := map[string]*pp.Function{}
	locs := map[string]*pp.Location{}
	loc := func(name string) *pp.Location {
		if l, ok := locs[name]; ok {
			return l
		}
		f := &pp.Function{ID: uint64(len(fns) + 1), Name: name, SystemName: name, Filename: "/src/" + name + ".go"}
		fns[name] = f
		p.Function = append(p.Function, f)
		l := &pp.Location{ID: uint64(len(locs) + 1), Line: []pp.Line{{Function: f, Line: 1}}}
		locs[name] = l
		p.Location = append(p.Location, l)
		return l
	}
	for _, s := range spec {
		if s.n <= 0 {
			continue
		}
		sm := &pp.Sample{Value: []int64{int64(s.n), int64(s.n) * 10_000_000}}
		for _, f := range s.frames {
			sm.Location = append(sm.Location, loc(f))
		}
		if s.span != "" {
			sm.Label = map[string][]string{"span_name": {s.span}}
		}
		p.Sample = append(p.Sample, sm)
	}
	var buf bytes.Buffer
	if err := p.Write(&buf); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func runMetaJSON(requests int, cpuMsPerReq float64, steal, throttled float64) string {
	wall := 20e9
	return fmt.Sprintf(`{"requests": %d, "os_cpu_ns": %.0f, "wall_ns": %.0f, "cores": 2, "steal_ns": %.0f, "throttled_ns": %.0f}`+"\n",
		requests, cpuMsPerReq*1e6*float64(requests), wall, steal, throttled)
}

// cpuProfileJSON is a hand-shaped Chrome .cpuprofile (hitCount form, no samples array):
// handleOrders 60 % (own, closure child 10 % of it), a span__orders_list frame around dbQuery,
// native JSON.stringify under handleOrders, and idle time that is not CPU.
func cpuProfileJSON(handle, db, idle int) string {
	return fmt.Sprintf(`{
  "nodes": [
    {"id": 1, "callFrame": {"functionName": "(root)", "url": ""}, "children": [2, 3]},
    {"id": 2, "callFrame": {"functionName": "(idle)", "url": ""}, "hitCount": %d},
    {"id": 3, "callFrame": {"functionName": "handleOrders", "url": "file:///app/src/orders.ts"}, "hitCount": %d, "children": [4, 6, 7]},
    {"id": 4, "callFrame": {"functionName": "span__orders_list", "url": "file:///app/src/orders.ts"}, "hitCount": 0, "children": [5]},
    {"id": 5, "callFrame": {"functionName": "dbQuery", "url": "file:///app/src/db.ts"}, "hitCount": %d},
    {"id": 6, "callFrame": {"functionName": "JSON.stringify", "url": ""}, "hitCount": 100},
    {"id": 7, "callFrame": {"functionName": "", "url": "file:///app/src/orders.ts"}, "hitCount": 100}
  ]
}
`, idle, handle, db)
}

// speedscopeJSON is a hand-shaped sampled speedscope file: render and query frames under a
// handler, one frame in a library, every sample weighted 10 ms.
func speedscopeJSON(render, query, lib int) string {
	var sb strings.Builder
	sb.WriteString(`{
  "$schema": "https://www.speedscope.app/file-format-schema.json",
  "shared": {"frames": [
    {"name": "main", "file": "app/main.dart"},
    {"name": "Handler.serve", "file": "app/handler.dart"},
    {"name": "render", "file": "app/render.dart"},
    {"name": "query", "file": "app/db.dart"},
    {"name": "dart:convert.jsonEncode", "file": "dart:convert"},
    {"name": "UserTag:checkout_page", "file": ""}
  ]},
  "profiles": [{"type": "sampled", "name": "main isolate", "unit": "milliseconds", "startValue": 0, "endValue": 0,
    "samples": [`)
	var samples []string
	add := func(n int, s string) {
		for i := 0; i < n; i++ {
			samples = append(samples, s)
		}
	}
	add(render, "[0,1,5,2]")
	add(query, "[0,1,3]")
	add(lib, "[0,1,2,4]")
	sb.WriteString(strings.Join(samples, ","))
	sb.WriteString(`],
    "weights": [`)
	ws := make([]string, len(samples))
	for i := range ws {
		ws[i] = "10"
	}
	sb.WriteString(strings.Join(ws, ","))
	sb.WriteString("]}]\n}\n")
	return sb.String()
}

// TestWriteFixtures regenerates tests/fixtures/profile when OBS_WRITE_FIXTURES names a directory.
//
//	OBS_WRITE_FIXTURES=$PWD/../../tests/fixtures/profile go test ./internal/profile -run TestWriteFixtures
func TestWriteFixtures(t *testing.T) {
	dir := os.Getenv("OBS_WRITE_FIXTURES")
	if dir == "" {
		t.Skip("set OBS_WRITE_FIXTURES to a directory to regenerate the e2e fixtures")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	w := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	wp := func(name string, spec []stackSpec, meta string) {
		w(name, string(pprofBytes(t, spec)))
		w(name+".run.json", meta)
	}
	b := baseSpec()
	// Three baseline runs: +-2 % noise, 1 ms CPU per request, quiet runner.
	wp("base-1.pb.gz", jitter(b, 1), runMetaJSON(600, 1.00, 1e6, 0))
	wp("base-2.pb.gz", jitter(b, 2), runMetaJSON(600, 0.98, 2e6, 0))
	wp("base-3.pb.gz", jitter(b, 3), runMetaJSON(600, 1.02, 0, 1e6))
	// A run like the baseline.
	wp("now-ok.pb.gz", jitter(b, 4), runMetaJSON(600, 1.01, 1e6, 0))
	// render.Page 50 % slower: its function row and the span "GET /browse" are slower; total +18 %.
	wp("now-slow.pb.gz", jitter(variant(b, map[string]float64{"svc/render.Page": 1.5}), 5), runMetaJSON(600, 1.18, 1e6, 0))
	// cache.Get (3.5 % of CPU, ~210 samples) rises by a fifth: inside 3x sampling noise.
	wp("now-noise.pb.gz", jitter(variant(b, map[string]float64{"svc/cache.Get": 1.2}), 6), runMetaJSON(600, 1.01, 1e6, 0))
	// render.Page 2.5x slower: more than 30 % and more than 60 % up in share.
	wp("now-slow-x2.pb.gz", jitter(variant(b, map[string]float64{"svc/render.Page": 2.5}), 8), runMetaJSON(600, 1.40, 1e6, 0))
	// cache.Get 2.5x faster (it is a small row, so its siblings' shares barely move): reported as faster, never failed.
	// (A big win in one function raises every sibling's share, because rows are shares of CPU: README, "What a share can and cannot see".)
	wp("now-faster.pb.gz", jitter(variant(b, map[string]float64{"svc/cache.Get": 0.4}), 7), runMetaJSON(600, 0.99, 1e6, 0))
	// Same profile as now-ok, but total CPU 20 % up: on a quiet runner and on a noisy one.
	w("run-total-slow-quiet.json", runMetaJSON(600, 1.20, 1e6, 0))
	w("run-total-slow-noisy.json", runMetaJSON(600, 1.20, 1.5e9, 1e9)) // 6.25 % of 2 cores x 20 s lost
	w("run-total-slow-unknown.json", `{"requests": 600, "os_cpu_ns": 720000000}`+"\n")
	// JS (Bun) .cpuprofile and a Dart-style speedscope file.
	w("base.cpuprofile", cpuProfileJSON(3000, 1500, 400))
	w("now-slow.cpuprofile", cpuProfileJSON(3000, 3000, 400))
	w("base.speedscope.json", speedscopeJSON(240, 200, 60))
	w("now-slow.speedscope.json", speedscopeJSON(480, 200, 60))
	for name, slow := range map[string]bool{"dotnet-base.speedscope.json.gz": false, "dotnet-slow.speedscope.json.gz": true} {
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		_, _ = zw.Write([]byte(dotnetEventedJSON(slow)))
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		w(name, buf.String())
	}
	for name, body := range map[string]string{"coalesced-base.speedscope.json.gz": coalescedEventedJSON(200, 800), "coalesced-slow.speedscope.json.gz": coalescedEventedJSON(300, 700)} {
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		_, _ = zw.Write([]byte(body))
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		w(name, buf.String())
	}
}

// quick structural check that the JSON fixtures are valid JSON.
func TestFixtureJSONIsValid(t *testing.T) {
	for _, s := range []string{cpuProfileJSON(1, 2, 3), speedscopeJSON(1, 2, 3)} {
		var v any
		if err := json.Unmarshal([]byte(s), &v); err != nil {
			t.Fatal(err)
		}
	}
}
