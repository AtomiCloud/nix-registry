package profile

import (
	"fmt"
	"strings"
	"testing"
)

func shiftedSummary(t *testing.T, root string, line, render int) *Summary {
	t.Helper()
	body := fmt.Sprintf(`{"nodes":[{"id":1,"callFrame":{"functionName":"render","url":"%s/src/a.js","lineNumber":%d},"hitCount":%d},{"id":2,"callFrame":{"functionName":"other","url":"%s/src/b.js","lineNumber":1},"hitCount":%d}]}`, root, line, render, root, 10000-render)
	p, err := readCPUProfile("shift.cpuprofile", []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	p.Format = FormatCPUProfile
	s, err := Summarize(p, nil, Options{Own: []string{"/src/"}})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestLineAndCheckoutShiftCannotHideRegression(t *testing.T) {
	base := shiftedSummary(t, "/checkout/base", 9, 2000)
	b, err := BuildBaseline("s", "n", "", []string{"/src/"}, []*Summary{base, base, base})
	if err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{"/checkout/base", "file:///different/checkout"} {
		rows, _ := Compare(b, shiftedSummary(t, root, 12, 2000), nil)
		for _, r := range rows {
			if r.Failed() || r.Missing {
				t.Errorf("pure shift must be green and matched: %+v", r)
			}
		}
		rows, extra := Compare(b, shiftedSummary(t, root, 12, 3000), nil)
		var out strings.Builder
		if PrintCompare(&out, "shift.baseline.json", rows, extra) != 1 || !strings.Contains(out.String(), "+50.0%") {
			t.Fatalf("line/checkout shift hid planted regression:\n%s", out.String())
		}
	}
}

func TestMissingHotBaselineFunctionIsAnError(t *testing.T) {
	base := shiftedSummary(t, "/checkout/base", 9, 2000)
	b, _ := BuildBaseline("s", "n", "", []string{"/src/"}, []*Summary{base, base, base})
	now := shiftedSummary(t, "/checkout/next", 12, 0)
	rows, extra := Compare(b, now, nil)
	var out strings.Builder
	if PrintCompare(&out, "missing.baseline.json", rows, extra) == 0 || !strings.Contains(out.String(), "--update") {
		t.Fatalf("missing baseline function must fail with update guidance:\n%s", out.String())
	}
}

func TestDuplicateDeclarationsMatchSourceOrderOnce(t *testing.T) {
	b := &Baseline{Functions: []Row{{Name: "render", File: "a.js", Line: 10, Share: .2, N: 2000}, {Name: "render", File: "a.js", Line: 50, Share: .3, N: 3000}}}
	now := &Summary{Functions: []FuncRow{{Name: "render", File: "a.js", Line: 53, Own: true, OwnShare: .3, OwnN: 3000}, {Name: "render", File: "a.js", Line: 13, Own: true, OwnShare: .2, OwnN: 2000}}}
	rows, _ := Compare(b, now, nil)
	for _, r := range rows {
		if r.Missing || r.Change != 0 {
			t.Fatalf("source-order one-to-one match failed: %+v", r)
		}
	}
}

func coalescedEventedJSON(render, other float64) string {
	var events []string
	at := 0.0
	for i := 0; i < 10; i++ {
		for frame, duration := range []float64{render, other} {
			events = append(events, fmt.Sprintf(`{"type":"O","frame":%d,"at":%g},{"type":"C","frame":%d,"at":%g}`, frame, at, frame, at+duration))
			at += duration
		}
	}
	return fmt.Sprintf(`{"shared":{"frames":[{"name":"App.Render"},{"name":"App.Other"}]},"profiles":[{"type":"evented","unit":"milliseconds","startValue":0,"endValue":%g,"events":[%s]}]}`, at, strings.Join(events, ","))
}

func TestCoalescedIntervalsUseSamplingPeriod(t *testing.T) {
	mk := func(render, other float64) *Summary {
		t.Helper()
		p, err := readSpeedscope("coalesced.json", []byte(coalescedEventedJSON(render, other)))
		if err != nil {
			t.Fatal(err)
		}
		p.Format = FormatSpeedscope
		s, err := Summarize(p, nil, Options{Own: []string{`^App\.`}})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	base := mk(200, 800)
	if base.Samples != 10000 {
		t.Errorf("10 seconds at 1ms sampling = 10000 samples, got %d", base.Samples)
	}
	b, err := BuildBaseline("d", "load", ProfilerDotnetTrace, []string{`^App\.`}, []*Summary{base, base, base})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b.Marshal()), `"sample_period_ns": 1000000`) {
		t.Errorf("baseline must store period: %s", b.Marshal())
	}
	rows, _ := Compare(b, base, nil)
	for _, r := range rows {
		if r.Failed() {
			t.Fatalf("green control: %+v", r)
		}
	}
	rows, _ = Compare(b, mk(300, 700), nil)
	if r := rowOf(rows, "function", "App.Render"); !r.Failed() || r.Limit != .3 {
		t.Fatalf("coalesced +50%% must fail the 30%% limit: %+v", r)
	}
}

func TestMinifiedDeclarationsKeepColumns(t *testing.T) {
	mk := func(a, b int) *Summary {
		t.Helper()
		body := fmt.Sprintf(`{"nodes":[{"id":1,"callFrame":{"functionName":"render","url":"/app/bundle.js","lineNumber":0,"columnNumber":10},"hitCount":%d},{"id":2,"callFrame":{"functionName":"render","url":"/app/bundle.js","lineNumber":0,"columnNumber":100},"hitCount":%d},{"id":3,"callFrame":{"functionName":"other","url":"/app/bundle.js","lineNumber":0,"columnNumber":200},"hitCount":60000}]}`, a, b)
		p, err := readCPUProfile("minified.cpuprofile", []byte(body))
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
		t.Fatalf("minified declarations collapsed: %+v", base.Functions)
	}
	b, err := BuildBaseline("s", "minified", "", []string{"/app/"}, []*Summary{base, base, base})
	if err != nil {
		t.Fatal(err)
	}
	rows, _ := Compare(b, base, nil)
	for _, r := range rows {
		if r.Failed() {
			t.Fatalf("green: %+v", r)
		}
	}
	rows, extra := Compare(b, mk(30000, 10000), nil)
	var out strings.Builder
	if PrintCompare(&out, "minified.baseline.json", rows, extra) != 1 || !strings.Contains(out.String(), "bundle.js:1:11") || !strings.Contains(out.String(), "+50.0%") {
		t.Fatalf("minified +50%% regression hidden:\n%s", out.String())
	}
	l, err := LoadLimits(write(t, t.TempDir(), "columns.yaml", []byte("overrides:\n  - {service: s, kind: function, name: render, file: app/bundle.js, line: 1, column: 101, limit: 0.9, why: other declaration only}\n")))
	if err != nil {
		t.Fatal(err)
	}
	rows, _ = Compare(b, mk(30000, 10000), l)
	for _, r := range rows {
		if r.Name == "render" && r.Column == 11 && !r.Failed() {
			t.Fatalf("column override leaked: %+v", r)
		}
	}
	l.Overrides[0].Column = 11
	rows, _ = Compare(b, mk(30000, 10000), l)
	for _, r := range rows {
		if r.Name == "render" && r.Column == 11 && (r.Failed() || r.Limit != .9) {
			t.Fatalf("column override not selected: %+v", r)
		}
	}
}

func TestBaselineMatchesShiftedRunsAndStoresMedian(t *testing.T) {
	runs := []*Summary{shiftedSummary(t, "/old/work", 9, 2000), shiftedSummary(t, "file:///other/work", 12, 2000), shiftedSummary(t, "/third/work", 15, 2000)}
	b, err := BuildBaseline("s", "n", "", []string{"/src/"}, runs)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Functions) != 2 {
		t.Fatalf("shifts manufactured baseline functions: %+v", b.Functions)
	}
	for _, f := range b.Functions {
		if f.Name == "render" && f.Share != .2 {
			t.Fatalf("shifted median: %+v", f)
		}
	}
}

func TestSamplePeriodMismatchCannotCompareOrBuildBaseline(t *testing.T) {
	p, err := readSpeedscopeWithPeriod("coalesced.json", []byte(coalescedEventedJSON(200, 800)), 2e6)
	if err != nil {
		t.Fatal(err)
	}
	p.Format = FormatSpeedscope
	s, err := Summarize(p, nil, Options{Own: []string{"App"}})
	if err != nil {
		t.Fatal(err)
	}
	if s.Samples != 5000 {
		t.Fatalf("2ms period: %d", s.Samples)
	}
	b, err := BuildBaseline("s", "n", "", []string{"App"}, []*Summary{s, s, s})
	if err != nil {
		t.Fatal(err)
	}
	now := *s
	now.SamplePeriodNs = 1e6
	rows, _ := Compare(b, &now, nil)
	if len(rows) != 1 || !rows[0].Failed() || rows[0].Kind != "input" {
		t.Fatalf("mismatch compared: %+v", rows)
	}
	if _, err := BuildBaseline("s", "n", "", []string{"App"}, []*Summary{s, s, &now}); err == nil || !strings.Contains(err.Error(), "sample periods") {
		t.Fatalf("mixed periods accepted: %v", err)
	}
}

func TestNormalizationPreservesDifferentSourceDirectories(t *testing.T) {
	p := &Profile{Format: FormatCPUProfile, Samples: []Sample{
		{Stack: []Frame{{Name: "render", File: "/checkout/service/client/src/index.js", Line: 1}}, Count: 20000, Weight: 20000},
		{Stack: []Frame{{Name: "render", File: "/checkout/service/server/src/index.js", Line: 1}}, Count: 20000, Weight: 20000},
		{Stack: []Frame{{Name: "other", File: "/checkout/service/other.js", Line: 1}}, Count: 60000, Weight: 60000},
	}}
	s, err := Summarize(p, nil, Options{Own: []string{"/checkout/service/"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Functions) != 3 {
		t.Fatalf("source normalization collapsed different directories: %+v", s.Functions)
	}
}
