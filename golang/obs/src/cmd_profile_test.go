package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProfileBaselineRequiresOwn(t *testing.T) {
	var out, stderr bytes.Buffer
	rc := runProfileBaseline([]string{"--format", "pprof", "--service", "s", "--scenario", "n", "a.pb", "b.pb", "c.pb"}, &out, &stderr)
	if rc != 2 || !strings.Contains(stderr.String(), "--own") {
		t.Fatalf("want exit 2 naming --own before reading profiles: rc %d: %s", rc, stderr.String())
	}
}

func TestEventedSamplePeriodFlagAndMismatch(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "evented.json")
	body := `{"shared":{"frames":[{"name":"App.Render"}]},"profiles":[{"type":"evented","unit":"milliseconds","startValue":0,"endValue":10000,"events":[{"type":"O","frame":0,"at":0},{"type":"C","frame":0,"at":10000}]}]}`
	if err := os.WriteFile(input, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	rc := runProfileBaseline([]string{"--format", "speedscope", "--service", "s", "--scenario", "n", "--own", "App", "--sample-period", "2ms", "--dir", dir, input, input, input}, &out, &stderr)
	if rc != 0 {
		t.Fatalf("custom-period baseline: %d %s", rc, stderr.String())
	}
	baseline := filepath.Join(dir, "s.n.json")
	data, err := os.ReadFile(baseline)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(`"sample_period_ns": 2000000`)) {
		t.Fatalf("period not stored: %s", data)
	}
	for _, tc := range []struct {
		period string
		want   int
	}{{"2ms", 0}, {"1ms", 2}} {
		out.Reset()
		stderr.Reset()
		rc = runProfileCompare([]string{"--format", "speedscope", "--baseline", baseline, "--sample-period", tc.period, input}, &out, &stderr)
		if rc != tc.want || (rc == 2 && !strings.Contains(stderr.String(), "sample period differs")) {
			t.Fatalf("period %s: rc%d: %s%s", tc.period, rc, out.String(), stderr.String())
		}
	}
}

func TestSourceRootFlagIsStoredAndMismatchesAreRefused(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "profile.json")
	if err := os.WriteFile(input, []byte(`{"nodes":[{"id":1,"callFrame":{"functionName":"svc/render","url":"/checkout/service/render.js"},"hitCount":10000}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	rc := runProfileSummarize([]string{"--format", "cpuprofile", "--own", "^svc/", input}, &out, &stderr)
	if rc != 0 || !strings.Contains(out.String(), "single unanchored path") || !strings.Contains(out.String(), "/checkout/service/render.js") || !strings.Contains(out.String(), "--source-root") {
		t.Fatalf("ambiguous inference must note the path without failing: rc%d %s%s", rc, out.String(), stderr.String())
	}
	out.Reset()
	stderr.Reset()
	rc = runProfileBaseline([]string{"--format", "cpuprofile", "--service", "s", "--scenario", "n", "--own", "^svc/", "--source-root", "/checkout/service", "--dir", dir, input, input, input}, &out, &stderr)
	if rc != 0 {
		t.Fatalf("source-root baseline: rc%d %s", rc, stderr.String())
	}
	baseline := filepath.Join(dir, "s.n.json")
	data, err := os.ReadFile(baseline)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(`"source_root": "/checkout/service"`)) || !strings.Contains(out.String(), "source root: /checkout/service (explicit)") {
		t.Fatalf("root not stored/reported: %s %s", data, out.String())
	}
	for _, tc := range []struct {
		root string
		want int
	}{{"/checkout/service", 0}, {"", 0}, {"/another/service", 2}} {
		args := []string{"--format", "cpuprofile", "--baseline", baseline, input}
		if tc.root != "" {
			args = append(args, "--source-root", tc.root)
		}
		out.Reset()
		stderr.Reset()
		rc = runProfileCompare(args, &out, &stderr)
		if rc != tc.want || (rc == 2 && !strings.Contains(stderr.String(), "explicit source roots differ")) {
			t.Fatalf("root %s: rc%d %s%s", tc.root, rc, out.String(), stderr.String())
		}
	}
}
