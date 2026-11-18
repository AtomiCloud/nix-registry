package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const checkClusterDoc = `service: cluster
types:
  pod: { kind: estimate, min: 28, max: 110, churn_per_day: 25 }
  phase: { kind: enum, values: [Pending, Running, Failed] }
metrics:
  kube_pod_status_phase:
    source: kube-state-metrics
    kind: gauge
    budget: 330
    used_by: [hub/alert/pod-crashloop, hub/dash/pods]
    labels:
      pod: pod
      uid: { follows: pod }
      phase: phase
  kube_pod_container_status_running:
    source: kube-state-metrics
    kind: gauge
    budget: 110
    labels:
      pod: pod
`

func repo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	for p, body := range files {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func runChk(args ...string) (int, string) {
	var out, errb bytes.Buffer
	rc := runCheck(args, &out, &errb)
	return rc, out.String() + errb.String()
}

func TestCheckPrintsSourceEntries(t *testing.T) {
	root := repo(t, map[string]string{"apps/cluster/observability/contract.yaml": checkClusterDoc})
	rc, out := runChk("--dir", filepath.Join(root, "apps/cluster/observability"))
	if rc != 0 {
		t.Fatalf("rc %d\n%s", rc, out)
	}
	for _, want := range []string{
		// the worst case is the estimate max times the enum; the follows label adds nothing
		"metric kube_pod_status_phase (gauge): worst case 330 series (estimate max), budget 330; source kube-state-metrics; used by hub/alert/pod-crashloop, hub/dash/pods; about 75 new series a day from churn",
		"metric kube_pod_container_status_running (gauge): worst case 110 series (estimate max), budget 110; source kube-state-metrics; used by nobody named; about 25 new series a day from churn",
		"WARN ",
		"[used-by-missing]",
		"kube_pod_container_status_running",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "pod label dropped") {
		t.Errorf("a source metric is not aggregated, so no pod-label note:\n%s", out)
	}
	// a warning is not a failure: only the metric with used_by is quiet
	if strings.Contains(out, "WARN kube_pod_status_phase") || strings.Count(out, "[used-by-missing]") != 1 {
		t.Errorf("warnings:\n%s", out)
	}
}

func TestCheckOverBudgetUsesEstimateMax(t *testing.T) {
	root := repo(t, map[string]string{"apps/cluster/observability/contract.yaml": strings.Replace(checkClusterDoc, "budget: 330", "budget: 329", 1)})
	rc, out := runChk("--dir", filepath.Join(root, "apps/cluster/observability"))
	if rc != 1 || !strings.Contains(out, "[over-budget]") || !strings.Contains(out, "worst case is 330 series, over its budget of 329") {
		t.Fatalf("rc %d\n%s", rc, out)
	}
}

func TestCheckEstimateOnOwnMetricNamesFileAndLine(t *testing.T) {
	doc := "service: s\ntypes:\n  pod: { kind: estimate, min: 1, max: 5, churn_per_day: 0 }\nmetrics:\n  s.things:\n    kind: counter\n    budget: 5\n    labels:\n      pod: pod\n"
	root := repo(t, map[string]string{"apps/s/observability/contract.yaml": doc})
	rc, out := runChk("--dir", filepath.Join(root, "apps/s/observability"))
	if rc != 1 || !strings.Contains(out, "[estimate-not-source]") || !strings.Contains(out, "apps/s/observability/contract.yaml:9") {
		t.Fatalf("rc %d\n%s", rc, out)
	}
}

// obs check reads every contract of the repository, prints each, and refuses a name two declare
func TestCheckReadsEveryContract(t *testing.T) {
	box := "service: box\ntypes:\n  lane: { kind: enum, values: [a, b] }\nmetrics:\n  box.jobs:\n    kind: counter\n    budget: 2\n    labels: { lane: lane }\n"
	root := repo(t, map[string]string{
		"apps/box/observability/contract.yaml":     box,
		"apps/cluster/observability/contract.yaml": checkClusterDoc,
	})
	dir := filepath.Join(root, "apps/box/observability")
	rc, out := runChk("--dir", dir)
	if rc != 0 || !strings.Contains(out, "metric box.jobs (counter)") || !strings.Contains(out, "metric kube_pod_status_phase (gauge)") || !strings.Contains(out, "contract apps/cluster/observability/contract.yaml") {
		t.Fatalf("rc %d\n%s", rc, out)
	}
	rc, out = runChk("--dir", dir, "--contracts", "none")
	if rc != 0 || strings.Contains(out, "kube_pod_status_phase") {
		t.Fatalf("--contracts none rc %d\n%s", rc, out)
	}
	clash := strings.Replace(box, "box.jobs", "kube_pod_status_phase", 1)
	if err := os.WriteFile(filepath.Join(dir, "contract.yaml"), []byte(strings.Replace(clash, "kind: counter", "kind: gauge\n    across_pods: max", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	rc, out = runChk("--dir", dir)
	if rc != 1 || !strings.Contains(out, "[metric-clash]") || !strings.Contains(out, "apps/box/observability/contract.yaml") || !strings.Contains(out, "apps/cluster/observability/contract.yaml") {
		t.Fatalf("clash rc %d\n%s", rc, out)
	}
}

// A warning from ANOTHER contract of the repository names that contract relative to the Git root: no
// absolute or temp path may be in any line of the output, so the message reads the same on every machine.
func TestCheckOtherContractWarningLineHasNoAbsolutePath(t *testing.T) {
	box := "service: box\ntypes:\n  lane: { kind: enum, values: [a, b] }\nmetrics:\n  box.jobs:\n    kind: counter\n    budget: 2\n    labels: { lane: lane }\n"
	root := repo(t, map[string]string{
		"apps/box/observability/contract.yaml":     box,
		"apps/cluster/observability/contract.yaml": checkClusterDoc,
	})
	rc, out := runChk("--dir", filepath.Join(root, "apps/box/observability"))
	if rc != 0 {
		t.Fatalf("rc %d\n%s", rc, out)
	}
	warn := ""
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "WARN ") && strings.Contains(l, "[used-by-missing]") {
			warn = l
		}
	}
	if !strings.HasPrefix(warn, "WARN apps/cluster/observability/contract.yaml metrics.kube_pod_container_status_running: [used-by-missing] apps/cluster/observability/contract.yaml:") {
		t.Fatalf("the other contract's warning line is not relative to the Git root: %q\n%s", warn, out)
	}
	noAbs(t, root, out)
}

// The same for a metric clash: the FAIL line names the contract relative to the Git root.
func TestCheckClashLineHasNoAbsolutePath(t *testing.T) {
	box := "service: box\ntypes:\n  lane: { kind: enum, values: [a, b] }\nmetrics:\n  kube_pod_status_phase:\n    kind: gauge\n    across_pods: max\n    budget: 2\n    labels: { lane: lane }\n"
	root := repo(t, map[string]string{
		"apps/box/observability/contract.yaml":     box,
		"apps/cluster/observability/contract.yaml": checkClusterDoc,
	})
	rc, out := runChk("--dir", filepath.Join(root, "apps/box/observability"))
	if rc != 1 || !strings.Contains(out, "[metric-clash]") {
		t.Fatalf("rc %d\n%s", rc, out)
	}
	noAbs(t, root, out)
}

// noAbs fails when a WARN or FAIL line holds the repository's absolute (temp) path.
func noAbs(t *testing.T, root, out string) {
	t.Helper()
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if (strings.HasPrefix(l, "WARN ") || strings.HasPrefix(l, "FAIL ")) && (strings.Contains(l, root) || strings.Contains(l, os.TempDir())) {
			t.Errorf("absolute or temp path in a diagnostic line: %q", l)
		}
	}
}

// Review of PR 125, thread 3: obs check names a directory discovery could not read, and --contracts none does not look.
func TestCheckFailsWhenDiscoveryCannotReadADirectory(t *testing.T) {
	root := repo(t, map[string]string{
		"apps/cluster/observability/contract.yaml": checkClusterDoc,
		"apps/locked/observability/x":              "x",
	})
	locked := filepath.Join(root, "apps/locked")
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	if _, err := os.ReadDir(locked); err == nil {
		t.Skip("the directory is readable anyway (running as root)")
	}
	dir := filepath.Join(root, "apps/cluster/observability")
	rc, out := runChk("--dir", dir)
	if rc != 1 || !strings.Contains(out, "FAIL ") || !strings.Contains(out, "contract discovery") || !strings.Contains(out, "FAIL contract discovery: cannot read apps/locked: ") {
		t.Fatalf("rc %d\n%s", rc, out)
	}
	rc, out = runChk("--dir", dir, "--contracts", "none")
	if rc != 0 || strings.Contains(out, "apps/locked") {
		t.Fatalf("--contracts none rc %d\n%s", rc, out)
	}
}
