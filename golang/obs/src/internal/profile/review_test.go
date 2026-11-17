package profile

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	pp "github.com/google/pprof/profile"
)

// Two render functions exchange CPU while their combined share stays unchanged.
// Neither source file nor a second declaration in the same file may collapse.
func TestSameNameFunctionsKeepIdentity(t *testing.T) {
	mk := func(a, b int) *Summary {
		t.Helper()
		body := fmt.Sprintf(`{"nodes":[
		{"id":1,"callFrame":{"functionName":"render","url":"/app/a.js","lineNumber":9},"hitCount":%d},
		{"id":2,"callFrame":{"functionName":"render","url":"/app/b.js","lineNumber":9},"hitCount":%d},
		{"id":3,"callFrame":{"functionName":"render","url":"/app/a.js","lineNumber":19},"hitCount":60000}]}`, a, b)
		p, err := readCPUProfile("same-name.cpuprofile", []byte(body))
		if err != nil {
			t.Fatal(err)
		}
		p.Format = FormatCPUProfile
		s, err := Summarize(p, nil, Options{Own: []string{"/app/"}})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	base := mk(20000, 20000)
	if len(base.Functions) != 3 {
		t.Fatalf("same-name functions collapsed: %+v", base.Functions)
	}
	b, err := BuildBaseline("s", "n", "", []string{"/app/"}, []*Summary{base, base, base})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Functions) != 3 {
		t.Fatalf("baseline collapsed functions: %+v", b.Functions)
	}
	rows, _ := Compare(b, mk(20000, 20000), nil)
	for _, r := range rows {
		if r.Failed() {
			t.Fatalf("green: %+v", r)
		}
	}
	rows, _ = Compare(b, mk(30000, 10000), nil)
	var out strings.Builder
	if n := PrintCompare(&out, "same-name.baseline.json", rows, nil); n != 1 {
		t.Fatalf("planted +50%% must fail exactly one function: %d\n%s", n, out.String())
	}
	for _, needle := range []string{"same-name.baseline.json", "a.js:10", "+50.0%", "5.0%"} {
		if !strings.Contains(out.String(), needle) {
			t.Errorf("missing %q: %s", needle, out.String())
		}
	}
}

func TestFunctionSourceThroughPprofRecursion(t *testing.T) {
	a := &pp.Function{ID: 1, Name: "render", Filename: "/app/a.go", StartLine: 10}
	b := &pp.Function{ID: 2, Name: "render", Filename: "/app/b.go", StartLine: 20}
	locs := []*pp.Location{
		{ID: 1, Line: []pp.Line{{Function: a, Line: 11}}},
		{ID: 2, Line: []pp.Line{{Function: a, Line: 12}}},
		{ID: 3, Line: []pp.Line{{Function: b, Line: 21}}},
	}
	p := &pp.Profile{SampleType: []*pp.ValueType{{Type: "samples", Unit: "count"}}, Function: []*pp.Function{a, b}, Location: locs,
		Sample: []*pp.Sample{{Location: locs, Value: []int64{100}}}}
	var buf bytes.Buffer
	if err := p.Write(&buf); err != nil {
		t.Fatal(err)
	}
	parsed, err := readPprof("recursion.pb.gz", buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	s, err := Summarize(parsed, nil, Options{Own: []string{"/app/"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Functions) != 2 {
		t.Fatalf("instruction lines split a function, or files merged: %+v", s.Functions)
	}
	for _, f := range s.Functions {
		if f.InclShare != 1 || (strings.HasSuffix(f.File, "/a.go") && f.Line != 10) || (strings.HasSuffix(f.File, "/b.go") && f.Line != 20) {
			t.Errorf("recursion must deduplicate declaration identity: %+v", f)
		}
	}
}

func TestSpeedscopeSourceIdentity(t *testing.T) {
	p, err := readSpeedscope("source.json", []byte(`{"shared":{"frames":[{"name":"render","file":"a.dart","line":10},{"name":"render","file":"a.dart","line":20},{"name":"render","file":"b.dart","line":10}]},"profiles":[{"type":"sampled","samples":[[0,1,2,2]]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	s, err := Summarize(p, nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Functions) != 3 {
		t.Fatalf("source identity: %+v", s.Functions)
	}
	for _, f := range s.Functions {
		if f.InclShare != 1 {
			t.Errorf("recursive identity: %+v", f)
		}
	}
}

func TestFunctionOverrideSelectsDeclaration(t *testing.T) {
	b := &Baseline{Service: "s", Functions: []Row{
		{Name: "render", File: "a.js", Line: 10, Share: .2, N: 20000},
		{Name: "render", File: "a.js", Line: 20, Share: .2, N: 20000},
		{Name: "render", File: "b.js", Line: 10, Share: .2, N: 20000},
	}}
	now := &Summary{}
	for _, r := range b.Functions {
		now.Functions = append(now.Functions, FuncRow{Name: r.Name, File: r.File, Line: r.Line, Own: true, OwnShare: .3, OwnN: 30000})
	}
	l, err := LoadLimits(write(t, t.TempDir(), "limits.yaml", []byte("overrides:\n  - {service: s, kind: function, name: render, file: a.js, line: 10, limit: 0.6, why: one declaration only}\n")))
	if err != nil {
		t.Fatal(err)
	}
	rows, _ := Compare(b, now, l)
	for _, r := range rows {
		selected := r.File == "a.js" && r.Line == 10
		if r.Failed() == selected || (selected && r.Limit != .6) {
			t.Errorf("override leaked or was not selected: %+v", r)
		}
	}
}

// TraceEvent writes one evented profile per thread, using O/C events and milliseconds.
func dotnetEventedJSON(slow bool) string {
	var events []string
	at := 0.0
	add := func(frame, n int, duration float64) {
		for i := 0; i < n; i++ {
			events = append(events, fmt.Sprintf(`{"type":"O","frame":%d,"at":%g},{"type":"C","frame":%d,"at":%g}`, frame, at, frame, at+duration))
			at += duration
		}
	}
	a, b := 200, 200
	if slow {
		a, b = 300, 100
	}
	add(1, a, 2) // render: weight twice its sample count
	add(2, b, 2)
	add(3, 600, 2)
	add(4, 1, 1e7) // wait must not dilute the CPU regression
	return fmt.Sprintf(`{"shared":{"frames":[{"name":"Thread (1)"},{"name":"App.Render"},{"name":"App.Query"},{"name":"App.Other"},{"name":"UNMANAGED_CODE_TIME"}]},"profiles":[{"type":"evented","name":"Thread (1)","unit":"milliseconds","startValue":0,"endValue":%g,"events":[{"type":"O","frame":0,"at":0},%s,{"type":"C","frame":0,"at":%g}]}]}`, at, strings.Join(events, ","), at)
}

func TestDotnetEventedGreenAndRed(t *testing.T) {
	mk := func(slow bool) *Summary {
		t.Helper()
		p, err := readSpeedscope("dotnet.speedscope.json", []byte(dotnetEventedJSON(slow)))
		if err != nil {
			t.Fatal(err)
		}
		p.Format = FormatSpeedscope
		s, err := Summarize(p, nil, Options{Own: []string{`^App\.`}})
		if err != nil {
			t.Fatal(err)
		}
		if s.Samples != 2000 {
			t.Fatalf("CPU interval count = %d; wait must be dropped", s.Samples)
		}
		return s
	}
	base := mk(false)
	b, err := BuildBaseline("dotnet", "load", ProfilerDotnetTrace, []string{`^App\.`}, []*Summary{base, base, base})
	if err != nil {
		t.Fatal(err)
	}
	rows, _ := Compare(b, base, nil)
	for _, r := range rows {
		if r.Failed() {
			t.Fatalf("green: %+v", r)
		}
	}
	rows, _ = Compare(b, mk(true), nil)
	if r := rowOf(rows, "function", "App.Render"); !r.Failed() || !near(r.Change, .5, 1e-9) || r.Limit != .30 {
		t.Fatalf("planted dotnet regression: %+v", r)
	}
}

func TestEventedOpenTimeThreadsAndBadEvents(t *testing.T) {
	prefix := `{"shared":{"frames":[{"name":"app/parent"},{"name":"app/leaf"},{"name":"AWAIT_TIME"}]},"profiles":[`
	// Parent own time 1 ms, leaf 3 ms; another thread's wait 100 ms is dropped.
	green := `{"type":"evented","unit":"milliseconds","startValue":0,"endValue":4,"events":[{"type":"O","frame":0,"at":0},{"type":"O","frame":1,"at":1},{"type":"C","frame":1,"at":4},{"type":"C","frame":0,"at":4}]}`
	wait := `{"type":"evented","unit":"seconds","startValue":0,"endValue":0.1,"events":[{"type":"O","frame":2,"at":0},{"type":"C","frame":2,"at":0.1}]}`
	p, err := readSpeedscope("threads.json", []byte(prefix+green+","+wait+` ]}`))
	if err != nil {
		t.Fatal(err)
	}
	s, err := Summarize(p, nil, Options{Own: []string{"^app/"}})
	if err != nil {
		t.Fatal(err)
	}
	if s.Samples != 4 || len(s.Functions) != 2 || s.Functions[0].Name != "app/leaf" || s.Functions[0].OwnShare != .75 {
		t.Fatalf("open-time weighting / wait removal: %+v", s)
	}
	// A 1 ms CPU sample in another thread must have the same weight as 0.001 s.
	mixed := strings.Replace(wait, `"frame":2`, `"frame":1`, -1)
	mixed = strings.ReplaceAll(mixed, "0.1", "0.001")
	p, err = readSpeedscope("units.json", []byte(prefix+green+","+mixed+` ]}`))
	if err != nil {
		t.Fatal(err)
	}
	s, err = Summarize(p, nil, Options{Own: []string{"^app/"}})
	if err != nil || !near(s.Functions[0].OwnShare, .8, 1e-9) {
		t.Fatalf("thread units: %v %+v", err, s)
	}
	for name, bad := range map[string]string{
		"close": strings.Replace(green, `"type":"C","frame":1`, `"type":"C","frame":0`, 1),
		"order": strings.Replace(green, `"at":1`, `"at":5`, 1),
		"frame": strings.Replace(green, `"frame":1`, `"frame":99`, 1),
		"kind":  strings.Replace(green, `"type":"O"`, `"type":"X"`, 1),
		"open":  strings.Replace(green, `,{"type":"C","frame":0,"at":4}`, ``, 1),
	} {
		if _, err := readSpeedscope(name+".json", []byte(prefix+bad+` ]}`)); err == nil || !strings.Contains(err.Error(), name+".json") {
			t.Errorf("malformed evented %s: %v", name, err)
		}
	}
}

func TestBaselineCutoffAndSpanPresence(t *testing.T) {
	runs := []*Summary{}
	for i := 0; i < 3; i++ {
		r := &Summary{Format: FormatPprof, Samples: 100000}
		for _, share := range []float64{.025, .029, .03} {
			r.Functions = append(r.Functions, FuncRow{Name: fmt.Sprintf("svc/%.3f", share), Own: true, OwnShare: share, OwnN: int64(share * 100000)})
		}
		if i == 0 {
			r.Spans = append(r.Spans, SpanRow{Name: "one-of-three", Share: .2, N: 20000})
		}
		if i < 2 {
			r.Spans = append(r.Spans, SpanRow{Name: "two-of-three", Share: .2, N: 20000})
		}
		runs = append(runs, r)
	}
	b, err := BuildBaseline("s", "n", "", []string{"^svc/"}, runs)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Functions) != 1 || b.Functions[0].Name != "svc/0.030" {
		t.Fatalf("3%% cutoff: %+v", b.Functions)
	}
	if len(b.Spans) != 1 || b.Spans[0].Name != "two-of-three" {
		t.Fatalf("2-of-3 spans: %+v", b.Spans)
	}
}
