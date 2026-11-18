package check

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/VictoriaMetrics/metricsql"

	"github.com/AtomiCloud/obs/internal/contract"
	"github.com/AtomiCloud/obs/internal/files"
)

const clusterContract = `
service: cluster
types:
  pod: { kind: estimate, min: 1, max: 50, churn_per_day: 3 }
  phase: { kind: enum, values: [Pending, Running] }
metrics:
  kube_pod_status_phase:
    source: kube-state-metrics
    kind: gauge
    budget: 100
    used_by: [hub/dash/pods]
    labels:
      pod: pod
      uid: { follows: pod }
      phase: phase
`

func sourceNames(t *testing.T, expr string) []string {
	t.Helper()
	c, err := contract.Parse("c.yaml", []byte(clusterContract))
	if err != nil {
		t.Fatal(err)
	}
	if is := c.Validate(); len(is) > 0 {
		t.Fatal(is)
	}
	e, err := metricsql.Parse(expr)
	if err != nil {
		t.Fatal(err)
	}
	r := newReport()
	contractMetrics(r, &files.Query{File: "f", Where: "w", Expr: expr}, e, c)
	var out []string
	for _, f := range r.Findings {
		out = append(out, "["+f.Rule+"] "+f.Msg)
	}
	return out
}

// the pod-label rule is for our own metrics: a source metric keeps its pod keys
func TestSourceMetricMayGroupByPodKeys(t *testing.T) {
	for _, q := range []string{
		`sum by (k8s.pod.name) (kube_pod_status_phase)`,
		`kube_pod_status_phase{phase="Running", pod=~"web.*"}`,
		`count by (pod, uid) (kube_pod_status_phase == 1)`,
	} {
		if got := sourceNames(t, q); len(got) > 0 {
			t.Errorf("%s: want no findings, got %v", q, got)
		}
	}
	// but a label the entry never declared is still refused, naming it
	got := sourceNames(t, `kube_pod_status_phase{nodename="x"}`)
	if len(got) != 1 || !strings.Contains(got[0], "contract-undeclared-label") || !strings.Contains(got[0], "nodename") {
		t.Errorf("undeclared label on a source metric: %v", got)
	}
}

// a value that is not in an enum is still named; an estimate has no value list to compare with
func TestEstimateLabelHasNoValueCheck(t *testing.T) {
	if got := sourceNames(t, `kube_pod_status_phase{pod="anything-1234"}`); len(got) > 0 {
		t.Errorf("estimate label value refused: %v", got)
	}
	got := sourceNames(t, `kube_pod_status_phase{phase="Paused"}`)
	if len(got) != 1 || !strings.Contains(got[0], "Paused") {
		t.Errorf("enum value on a source metric: %v", got)
	}
}

func repoWith(t *testing.T, files map[string]string) string {
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

const boxContract = "service: box\nmetrics:\n  box.jobs:\n    kind: counter\n"

// obs query reads every contract of the repository: a box dashboard may use a cluster metric
func TestQueryLoadsEveryContractOfTheRepo(t *testing.T) {
	root := repoWith(t, map[string]string{
		"apps/box/observability/contract.yaml":     boxContract,
		"apps/cluster/observability/contract.yaml": clusterContract,
	})
	o := Options{Dir: filepath.Join(root, "apps/box/observability")}
	c, problems := loadContract(&o)
	if len(problems) != 0 || c == nil {
		t.Fatalf("problems %v", problems)
	}
	if _, ok := c.Lookup("kube_pod_status_phase"); !ok {
		t.Fatal("the cluster contract's metric is unknown to the box check")
	}
	if _, ok := c.Lookup("box_jobs_total"); !ok {
		t.Fatal("own metric lost")
	}
	// opting out
	o2 := Options{Dir: filepath.Join(root, "apps/box/observability"), NoOtherContracts: true}
	c2, _ := loadContract(&o2)
	if _, ok := c2.Lookup("kube_pod_status_phase"); ok {
		t.Fatal("NoOtherContracts still merged")
	}
}

func TestQueryClashNamesBothFiles(t *testing.T) {
	root := repoWith(t, map[string]string{
		"apps/box/observability/contract.yaml":     "service: box\nmetrics:\n  kube_pod_status_phase:\n    kind: gauge\n    across_pods: max\n",
		"apps/cluster/observability/contract.yaml": clusterContract,
	})
	o := Options{Dir: filepath.Join(root, "apps/box/observability")}
	_, problems := loadContract(&o)
	joined := strings.Join(problems, "\n")
	if !strings.Contains(joined, "metric-clash") || !strings.Contains(joined, "apps/box/observability/contract.yaml") || !strings.Contains(joined, "apps/cluster/observability/contract.yaml") {
		t.Fatalf("clash must name both files: %v", problems)
	}
}

func TestQueryInvalidOtherContractNamesItsFile(t *testing.T) {
	root := repoWith(t, map[string]string{
		"apps/box/observability/contract.yaml":     boxContract,
		"apps/cluster/observability/contract.yaml": strings.Replace(clusterContract, "budget: 100", "budget: 10", 1),
	})
	_, problems := loadContract(&Options{Dir: filepath.Join(root, "apps/box/observability")})
	if len(problems) == 0 || !strings.Contains(strings.Join(problems, "\n"), "apps/cluster/observability/contract.yaml") || !strings.Contains(strings.Join(problems, "\n"), "over-budget") {
		t.Fatalf("problems %v", problems)
	}
}
