package main

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPruneForwardsAllParsedSettings(t *testing.T) {
	makeFlags := func() (*flag.FlagSet, *string, *bool, *int) {
		f := flag.NewFlagSet("fixture", flag.ContinueOnError)
		return f, f.String("vl-settings", "default", ""), f.Bool("enabled", false, ""), f.Int("workers", 0, "")
	}
	original, _, _, _ := makeFlags()
	if err := original.Parse([]string{"--vl-settings=--literal-path", "--enabled", "--workers=3"}); err != nil {
		t.Fatal(err)
	}
	child, setting, enabled, workers := makeFlags()
	if err := child.Parse(pruneForwardedFlags(original)); err != nil {
		t.Fatal(err)
	}
	if *setting != "--literal-path" || !*enabled || *workers != 3 {
		t.Fatalf("child lost settings: %s %v %d", *setting, *enabled, *workers)
	}
}

// A test binary is not obs: when OBS_TEST_AS_OBS is set (the test below sets it, so the prune
// self-check's two children inherit it) the process runs obs itself instead of the tests.
func TestMain(m *testing.M) {
	if os.Getenv("OBS_TEST_AS_OBS") == "1" {
		os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
	}
	os.Exit(m.Run())
}

// The FULL (OBS_NO_PRUNE=1) child must receive the same forwarded flags as the pruned one.
// The project's default settings file refuses every log query (maxQueryLen 1); the explicit
// --vl-settings file allows it. A child that dropped --vl-settings would read the default,
// refuse, and exit 1 while the pruned child exits 0: an exit-status mismatch.
func TestPruneFullChildReceivesForwardedFlags(t *testing.T) {
	dir := project(t, map[string]string{"logs/warning.yaml": `apiVersion: grafana.integreatly.org/v1beta1
kind: GrafanaAlertRuleGroup
metadata: {name: settings}
spec:
  interval: 1m
  rules:
    - uid: r1
      condition: B
      data:
        - refId: A
          datasourceUid: fixture-logs
          relativeTimeRange: {from: 60, to: 0}
          model: {expr: '"durian.cloud/event":="checkout.charge.failed" | stats count() n', queryType: stats}
        - refId: B
          datasourceUid: __expr__
          model: {type: math, expression: '$A > 1000000'}
`})
	if err := os.WriteFile(filepath.Join(dir, "seed.yaml"), []byte(strings.Replace(seedDoc, "length: 1h", "length: 1m", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "victorialogs-settings.yaml"), []byte("search.maxQueryLen: 1"), 0o600); err != nil {
		t.Fatal(err)
	}
	allow := filepath.Join(t.TempDir(), "allow.yaml")
	if err := os.WriteFile(allow, []byte("search.maxQueryLen: 1KiB"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OBS_TEST_AS_OBS", "1")
	t.Setenv("OBS_CACHE_DIR", "")
	t.Setenv("OBS_VERIFY_PRUNE", "1")

	// Control: without the explicit file the default settings refuse in both children.
	if rc, out := obs("alerts", "--dir", dir, "--base", "1700000000"); rc != 1 || !strings.Contains(out, "-search.maxQueryLen=1") {
		t.Fatalf("default settings must refuse: rc=%d %s", rc, out)
	}
	rc, out := obs("alerts", "--dir", dir, "--base", "1700000000", "--vl-settings", allow)
	if rc != 0 || !strings.Contains(out, "mismatches=0") || strings.Contains(out, "MISMATCH") {
		t.Fatalf("the FULL prune child lost --vl-settings: rc=%d %s", rc, out)
	}
}
