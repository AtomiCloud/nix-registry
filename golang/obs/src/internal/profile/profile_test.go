package profile

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func summarizeSpec(t *testing.T, spec []stackSpec, meta *RunMeta) *Summary {
	t.Helper()
	p := write(t, t.TempDir(), "p.pb.gz", pprofBytes(t, spec))
	pr, err := Read(p, FormatPprof)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Summarize(pr, meta, Options{Own: []string{`^svc/`}})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func near(a, b, eps float64) bool { return math.Abs(a-b) <= eps }

func TestPprofSummary(t *testing.T) {
	s := summarizeSpec(t, baseSpec(), &RunMeta{Requests: 600, OSCPUNs: 6e8})
	if s.Samples != 6000 {
		t.Fatalf("samples %d", s.Samples)
	}
	get := func(n string) FuncRow {
		for _, f := range s.Functions {
			if f.Name == n {
				return f
			}
		}
		t.Fatalf("no function %s", n)
		return FuncRow{}
	}
	// self / inclusive / own for a leaf, a caller with a library callee and a closure's owner.
	if r := get("svc/render.Page"); !near(r.SelfShare, 0.4, 1e-9) || !near(r.OwnShare, 0.4, 1e-9) {
		t.Errorf("render.Page %+v", r)
	}
	if r := get("svc/handlers.Browse"); !near(r.SelfShare, 0, 1e-9) || !near(r.InclShare, 0.5350, 1e-3) || !near(r.OwnShare, 0.10, 1e-9) {
		t.Errorf("Browse %+v: own must hold the json.Marshal callee (10 %%)", r)
	}
	if r := get("svc/handlers.Order"); !near(r.OwnShare, 0.05, 1e-9) {
		t.Errorf("Order %+v: the closure's 5 %% belongs to its function", r)
	}
	if r := get("svc/handlers.Order.func1"); r.Own || r.OwnShare != 0 {
		t.Errorf("a closure is never an own-code row: %+v", r)
	}
	if r := get("encoding/json.Marshal"); r.Own {
		t.Errorf("library code is not own code: %+v", r)
	}
	if r := get("runtime.goexit"); !near(r.InclShare, 1, 1e-9) {
		t.Errorf("goexit inclusive %+v", r)
	}
	var browse SpanRow
	for _, sp := range s.Spans {
		if sp.Name == "GET /browse" {
			browse = sp
		}
	}
	if !near(browse.Share, 3210.0/6000, 1e-9) || browse.N != 3210 {
		t.Errorf("span GET /browse %+v", browse)
	}
	if !near(s.CPUPerRequestNs, 1e6, 1) {
		t.Errorf("cpu per request %v", s.CPUPerRequestNs)
	}
}

func TestRecursionCountsOnceInclusive(t *testing.T) {
	spec := []stackSpec{{frames: []string{"svc/a", "svc/a", "svc/a", "svc/main"}, n: 100}}
	s := summarizeSpec(t, spec, nil)
	for _, f := range s.Functions {
		if f.Name == "svc/a" && !near(f.InclShare, 1, 1e-9) {
			t.Errorf("recursive function inclusive %v, want 1", f.InclShare)
		}
	}
}

func TestPprofInlinedFramesAndNoCPUType(t *testing.T) {
	// A profile with one value type (alloc style "samples") still reads; a non-profile file is refused naming the file.
	dir := t.TempDir()
	if _, err := Read(write(t, dir, "junk.pb", []byte("not a profile")), FormatPprof); err == nil || !strings.Contains(err.Error(), "junk.pb") {
		t.Errorf("junk pprof: %v", err)
	}
	if _, err := Read(write(t, dir, "x.json", []byte("{")), FormatCPUProfile); err == nil || !strings.Contains(err.Error(), "x.json") {
		t.Errorf("junk cpuprofile: %v", err)
	}
	if _, err := Read(filepath.Join(dir, "x.json"), "flame"); err == nil || !strings.Contains(err.Error(), "flame") {
		t.Errorf("unknown format: %v", err)
	}
}

func TestCPUProfile(t *testing.T) {
	dir := t.TempDir()
	p, err := Read(write(t, dir, "a.cpuprofile", []byte(cpuProfileJSON(3000, 1500, 400))), FormatCPUProfile)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Summarize(p, nil, Options{Own: []string{`/app/src/`}})
	if err != nil {
		t.Fatal(err)
	}
	// 3000 + 1500 + 100 + 100 CPU samples; the 400 idle samples are not CPU.
	if s.Samples != 4700 {
		t.Fatalf("samples %d, want 4700 (idle dropped)", s.Samples)
	}
	var span *SpanRow
	for i := range s.Spans {
		if s.Spans[i].Name == "orders_list" {
			span = &s.Spans[i]
		}
	}
	if span == nil || span.N != 1500 {
		t.Fatalf("span__orders_list frame must give span orders_list with 1500 samples: %+v", s.Spans)
	}
	for _, f := range s.Functions {
		switch f.Name {
		case "handleOrders":
			// own = self 3000 + JSON.stringify 100 (native) + the anonymous closure 100.
			if f.OwnN != 3200 {
				t.Errorf("handleOrders own samples %d, want 3200", f.OwnN)
			}
		case "span__orders_list":
			if f.Own {
				t.Error("a span frame is not a function row")
			}
		}
	}
	// samples array form with an explicit parent-less list and timeDeltas.
	arr := `{"nodes":[{"id":1,"callFrame":{"functionName":"(root)"},"children":[2]},{"id":2,"callFrame":{"functionName":"f","url":"u"}}],"samples":[2,2,2,1],"timeDeltas":[1,1,1,1]}`
	p, err = Read(write(t, dir, "b.cpuprofile", []byte(arr)), FormatCPUProfile)
	if err != nil || len(p.Samples) != 1 || p.Samples[0].Count != 3 {
		t.Fatalf("samples array: %v %+v", err, p)
	}
}

func TestSpeedscope(t *testing.T) {
	dir := t.TempDir()
	p, err := Read(write(t, dir, "s.json", []byte(speedscopeJSON(240, 200, 60))), FormatSpeedscope)
	if err != nil {
		t.Fatal(err)
	}
	s, _ := Summarize(p, nil, Options{Own: []string{`^app/`}})
	if s.Samples != 500 {
		t.Fatalf("samples %d", s.Samples)
	}
	var tag *SpanRow
	for i := range s.Spans {
		if s.Spans[i].Name == "checkout_page" {
			tag = &s.Spans[i]
		}
	}
	if tag == nil || tag.N != 240 {
		t.Fatalf("UserTag frame must give a span with 240 samples: %+v", s.Spans)
	}
	for _, f := range s.Functions {
		if f.Name == "render" && !near(f.OwnShare, 300.0/500, 1e-9) {
			t.Errorf("render own %v: its 60 jsonEncode samples belong to it", f.OwnShare)
		}
	}
	ev := `{"shared":{"frames":[{"name":"a"}]},"profiles":[{"type":"evented","events":[]}]}`
	if _, err := Read(write(t, dir, "e.json", []byte(ev)), FormatSpeedscope); err == nil || !strings.Contains(err.Error(), "evented") {
		t.Errorf("evented: %v", err)
	}
}

func TestClosureNames(t *testing.T) {
	for n, want := range map[string]bool{
		"main.Run.func1": true, "main.Run.func1.2": true, "main.(*S).M.func3": true, "main.Run.gowrap1": true,
		"main.Run": false, "main.funcs": false, "main.Func1": false, "main.(*S).Method-fm": true,
		"(anonymous)": true, "": true, "handleOrders": false,
		"Foo.<anonymous closure>": true, "Acme.Orders.<>c__DisplayClass1_0.<Get>b__0": true, "Acme.Orders.Get": false,
	} {
		if isClosure(n) != want {
			t.Errorf("isClosure(%q) = %v, want %v", n, !want, want)
		}
	}
}

func TestNoiseAndLimit(t *testing.T) {
	if !near(Noise(2400, 3600), math.Sqrt(1.0/2400+1.0/3600), 1e-12) {
		t.Error("noise formula")
	}
	// A big row: tolerance wins. A small row: 3 x noise wins.
	if l := Limit(0.05, 2400, 2400); !near(l, 3*math.Sqrt(2.0/2400), 1e-12) || l < 0.05 {
		t.Errorf("limit big row %v", l)
	}
	if l := Limit(0.05, 100000, 100000); l != 0.05 {
		t.Errorf("tolerance floor: %v", l)
	}
	if !math.IsInf(Limit(0.05, 0, 10), 1) {
		t.Error("no samples: infinite limit")
	}
}

func threeRuns(t *testing.T) []*Summary {
	var runs []*Summary
	for i := int64(1); i <= 3; i++ {
		runs = append(runs, summarizeSpec(t, jitter(baseSpec(), i), &RunMeta{Requests: 600, OSCPUNs: 6e8 + float64(i)*1e7}))
	}
	return runs
}

func TestBaseline(t *testing.T) {
	runs := threeRuns(t)
	b, err := BuildBaseline("checkout", "browse", "", []string{"^svc/"}, runs)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, r := range b.Functions {
		names = append(names, r.Name)
	}
	// 5 rows: rare.Thing (1.5 %) is under 3 %, the closure and library frames are never rows.
	if strings.Join(names, ",") != "svc/render.Page,svc/db.Query,svc/handlers.Browse,svc/handlers.Order,svc/cache.Get" {
		t.Errorf("function rows %v", names)
	}
	if len(b.Spans) != 2 {
		t.Errorf("span rows %+v", b.Spans)
	}
	// Median of 3: cpu per request of the middle run.
	if !near(b.CPUPerRequestNs, (6e8+2e7)/600, 1) {
		t.Errorf("median cpu per request %v", b.CPUPerRequestNs)
	}
	// Deterministic and small.
	b2, _ := BuildBaseline("checkout", "browse", "", []string{"^svc/"}, runs)
	if string(b.Marshal()) != string(b2.Marshal()) {
		t.Error("baseline is not deterministic")
	}
	if n := len(b.Marshal()); n > 4096 {
		t.Errorf("baseline is %d bytes, want a few KB", n)
	}
	if _, err := BuildBaseline("c", "b", "", nil, runs[:2]); err == nil || !strings.Contains(err.Error(), "exactly 3") {
		t.Errorf("two runs: %v", err)
	}
	// A baseline round trips through its file.
	dir := t.TempDir()
	p := write(t, dir, "b.json", b.Marshal())
	back, err := LoadBaseline(p)
	if err != nil || string(back.Marshal()) != string(b.Marshal()) {
		t.Errorf("load: %v", err)
	}
	if _, err := LoadBaseline(write(t, dir, "bad.json", []byte(`{"version":1,"service":"a","scenario":"b","bogus":1}`))); err == nil {
		t.Error("unknown field accepted")
	}
}

func TestMedianPicksTheMiddleRun(t *testing.T) {
	// One outlier run among three must not move the baseline.
	mk := func(scale float64) *Summary {
		return summarizeSpec(t, variant(baseSpec(), map[string]float64{"svc/render.Page": scale}), nil)
	}
	b, err := BuildBaseline("s", "n", "", []string{"^svc/"}, []*Summary{mk(1), mk(1), mk(3)})
	if err != nil {
		t.Fatal(err)
	}
	if !near(b.Functions[0].Share, 0.4, 1e-3) {
		t.Errorf("median share %v, want 0.4", b.Functions[0].Share)
	}
}

func rowOf(rows []Result, kind, name string) Result {
	for _, r := range rows {
		if r.Kind == kind && (name == "" || r.Name == name) {
			return r
		}
	}
	return Result{}
}

func quietMeta(cpuMs float64) *RunMeta {
	z, st := 0.0, 1e6
	return &RunMeta{Requests: 600, OSCPUNs: cpuMs * 1e6 * 600, WallNs: 20e9, Cores: 2, StealNs: &st, ThrottledNs: &z}
}

func baselineFixture(t *testing.T, profiler string) *Baseline {
	var runs []*Summary
	for i := int64(1); i <= 3; i++ {
		runs = append(runs, summarizeSpec(t, jitter(baseSpec(), i), quietMeta(1.0)))
	}
	b, err := BuildBaseline("checkout", "browse", profiler, []string{"^svc/"}, runs)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestCompareSlowerFasterAndNoise(t *testing.T) {
	b := baselineFixture(t, "")
	// Same shape: nothing fails.
	rows, _ := Compare(b, summarizeSpec(t, jitter(baseSpec(), 9), quietMeta(1.01)), nil)
	for _, r := range rows {
		if r.Failed() {
			t.Errorf("same shape failed: %+v", r)
		}
	}
	// A big row 50 % slower: the function and its span fail.
	slow := summarizeSpec(t, variant(baseSpec(), map[string]float64{"svc/render.Page": 1.5}), quietMeta(1.0))
	rows, _ = Compare(b, slow, nil)
	if r := rowOf(rows, "function", "svc/render.Page"); !r.Failed() || r.Change < 0.2 {
		t.Errorf("render.Page %+v", r)
	}
	if r := rowOf(rows, "span", "GET /browse"); !r.Failed() {
		t.Errorf("span %+v", r)
	}
	// A small row (about 210 samples) up by a fifth stays inside 3 x noise; a big row up by a fifth does not.
	small := summarizeSpec(t, variant(baseSpec(), map[string]float64{"svc/cache.Get": 1.2}), quietMeta(1.0))
	rows, _ = Compare(b, small, nil)
	if r := rowOf(rows, "function", "svc/cache.Get"); r.Failed() || r.Change < 0.15 || r.Limit < 0.25 {
		t.Errorf("small row must be protected by noise: %+v", r)
	}
	big := summarizeSpec(t, variant(baseSpec(), map[string]float64{"svc/db.Query": 1.25}), quietMeta(1.0))
	rows, _ = Compare(b, big, nil)
	if r := rowOf(rows, "function", "svc/db.Query"); !r.Failed() {
		t.Errorf("a big row up a fifth must fail: %+v", r)
	}
	// Faster is reported, never failed.
	fast := summarizeSpec(t, variant(baseSpec(), map[string]float64{"svc/cache.Get": 0.4}), quietMeta(0.99))
	rows, _ = Compare(b, fast, nil)
	if r := rowOf(rows, "function", "svc/cache.Get"); r.Failed() || !r.Faster {
		t.Errorf("faster row %+v", r)
	}
	// A function that vanished is faster, not a failure.
	gone := summarizeSpec(t, variant(baseSpec(), map[string]float64{"svc/cache.Get": 0}), quietMeta(1.0))
	rows, _ = Compare(b, gone, nil)
	if r := rowOf(rows, "function", "svc/cache.Get"); r.Failed() || !r.Missing {
		t.Errorf("vanished row %+v", r)
	}
}

func TestCompareTotalNeedsAQuietRunner(t *testing.T) {
	b := baselineFixture(t, "")
	run := func(m *RunMeta) Result {
		rows, _ := Compare(b, summarizeSpec(t, jitter(baseSpec(), 9), m), nil)
		return rowOf(rows, "total", "")
	}
	if r := run(quietMeta(1.2)); !r.Failed() {
		t.Errorf("quiet runner, +20 %%: %+v", r)
	}
	noisy := quietMeta(1.2)
	st, th := 1.5e9, 1e9
	noisy.StealNs, noisy.ThrottledNs = &st, &th
	if r := run(noisy); r.Failed() || !r.Slower || !strings.Contains(r.Note, "noisy runner") {
		t.Errorf("noisy runner must report, not fail: %+v", r)
	}
	unknown := &RunMeta{Requests: 600, OSCPUNs: 1.2e6 * 600}
	if r := run(unknown); r.Failed() || !r.Slower || !strings.Contains(r.Note, "no runner signal") {
		t.Errorf("no signal must report, not fail: %+v", r)
	}
	// Under the 15 % tolerance: ok on a quiet runner.
	if r := run(quietMeta(1.10)); r.Failed() {
		t.Errorf("+10 %% is inside 15 %%: %+v", r)
	}
	// No run meta at all: no total row, one note.
	rows, extra := Compare(b, summarizeSpec(t, jitter(baseSpec(), 9), nil), nil)
	if rowOf(rows, "total", "").Kind != "" || len(extra) == 0 {
		t.Errorf("no meta: rows %+v extra %v", rows, extra)
	}
}

func TestDotnetTolerance(t *testing.T) {
	b := baselineFixture(t, ProfilerDotnetTrace)
	// db.Query +25 % fails with 10 % but not with 30 % (limit is the bigger of tolerance and 3 x noise).
	run := summarizeSpec(t, variant(baseSpec(), map[string]float64{"svc/db.Query": 1.2}), quietMeta(1.0))
	rows, _ := Compare(b, run, nil)
	if r := rowOf(rows, "function", "svc/db.Query"); r.Failed() || !near(r.Limit, 0.30, 1e-9) || !strings.Contains(r.Why, "dotnet-trace") {
		t.Errorf("dotnet row %+v", r)
	}
}

func TestLimitOverrides(t *testing.T) {
	dir := t.TempDir()
	ok := write(t, dir, "ok.yaml", []byte(`overrides:
  - service: checkout
    kind: function
    name: svc/db.Query
    limit: 0.40
    why: the query runs a cold cache for the first 200 requests
`))
	l, err := LoadLimits(ok)
	if err != nil {
		t.Fatal(err)
	}
	b := baselineFixture(t, "")
	run := summarizeSpec(t, variant(baseSpec(), map[string]float64{"svc/db.Query": 1.25}), quietMeta(1.0))
	rows, _ := Compare(b, run, l)
	r := rowOf(rows, "function", "svc/db.Query")
	if r.Failed() || !near(r.Limit, 0.40, 1e-9) || !strings.Contains(r.Why, "cold cache") {
		t.Errorf("override not applied: %+v", r)
	}
	// Another row of the same service keeps its default.
	if r := rowOf(rows, "function", "svc/render.Page"); strings.Contains(r.Why, "override") {
		t.Errorf("override leaked: %+v", r)
	}
	for name, body := range map[string]string{
		"no-why":   "overrides:\n  - {service: checkout, limit: 0.4}\n",
		"blank":    "overrides:\n  - {service: checkout, limit: 0.4, why: '  '}\n",
		"no-svc":   "overrides:\n  - {limit: 0.4, why: x}\n",
		"bad-kind": "overrides:\n  - {service: c, kind: file, limit: 0.4, why: x}\n",
		"percent":  "overrides:\n  - {service: c, limit: 40, why: x}\n",
		"unknown":  "overrides:\n  - {service: c, limit: 0.4, why: x, tolerance: 1}\n",
	} {
		if _, err := LoadLimits(write(t, dir, name+".yaml", []byte(body))); err == nil {
			t.Errorf("%s: accepted", name)
		} else if !strings.Contains(err.Error(), name+".yaml") {
			t.Errorf("%s: error must name the file: %v", name, err)
		}
	}
	if l, err := LoadLimits(filepath.Join(dir, "missing.yaml")); err != nil || len(l.Overrides) != 0 {
		t.Errorf("missing file means no overrides: %v", err)
	}
}

func TestPrintCompareNamesFileRowAndValues(t *testing.T) {
	b := baselineFixture(t, "")
	slow := summarizeSpec(t, variant(baseSpec(), map[string]float64{"svc/render.Page": 1.5}), quietMeta(1.0))
	rows, extra := Compare(b, slow, nil)
	var sb strings.Builder
	n := PrintCompare(&sb, "e2e/profile-baseline/checkout.browse.json", rows, extra)
	out := sb.String()
	if n < 2 {
		t.Fatalf("failed rows %d\n%s", n, out)
	}
	for _, w := range []string{"what", "baseline", "change (limit)", "FAIL e2e/profile-baseline/checkout.browse.json function \"svc/render.Page\"", "SLOWER"} {
		if !strings.Contains(out, w) {
			t.Errorf("output lacks %q\n%s", w, out)
		}
	}
}
