package aggregation

import (
	"strings"
	"testing"

	"github.com/AtomiCloud/obs/internal/contract"
	"gopkg.in/yaml.v3"
)

const doc = `
service: svc
types: { p: { kind: enum, values: [a, b] } }
metrics:
  requests: { kind: counter }
  request.duration: { kind: histogram, unit: seconds }
  in.flight: { kind: gauge, across_pods: sum }
  depth: { kind: gauge, across_pods: avg }
  lag: { kind: gauge, across_pods: max }
  gen: { kind: gauge, across_pods: last }
  runtime.memory: { kind: gauge, per_pod: true, unit: bytes }
  runtime.cpu: { kind: counter, per_pod: true }
`

func TestGenerate(t *testing.T) {
	c, err := contract.Parse("c.yaml", []byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	out := string(Generate(c, "# header\n"))
	var rules []map[string]any
	if err := yaml.Unmarshal([]byte(out), &rules); err != nil {
		t.Fatalf("not YAML: %v\n%s", err, out)
	}
	if len(rules) != 5 {
		t.Fatalf("want 5 rules (counter, sum, avg, max, last), got %d\n%s", len(rules), out)
	}
	if m := rules[0]["match"].(string); !strings.Contains(m, "requests_total") || !strings.Contains(m, "request_duration_seconds_bucket") || !strings.Contains(m, "request_duration_seconds_count") {
		t.Fatalf("counter match %s", m)
	}
	for _, r := range rules {
		if r["keep_metric_names"] != true {
			t.Fatalf("rule without keep_metric_names: %v", r)
		}
		if w := r["without"].([]any); len(w) != 4 {
			t.Fatalf("without %v", w)
		}
		if strings.Contains(r["match"].(string), "runtime") {
			t.Fatalf("per_pod metric in a match: %v", r["match"])
		}
	}
	if rules[1]["dedup_interval"] != "30s" || rules[1]["outputs"].([]any)[0] != "sum_samples" {
		t.Fatalf("gauge rule %v", rules[1])
	}
	if !strings.Contains(out, "Per pod (in no match, so stored unchanged): runtime.memory, runtime.cpu") {
		t.Fatal(out)
	}
}

// Opus 1 (concept 4.1): every rule flushes on shutdown, so a planned restart loses ~8% instead of ~17%.
func TestEveryRuleFlushesOnShutdown(t *testing.T) {
	c, err := contract.Parse("c.yaml", []byte(`
service: s
metrics:
  a: { kind: counter }
  g1: { kind: gauge, across_pods: max }
  g2: { kind: gauge, across_pods: sum }
`))
	if err != nil {
		t.Fatal(err)
	}
	out := string(Generate(c, "# h\n"))
	rules, flush := strings.Count(out, "- match:"), strings.Count(out, "  flush_on_shutdown: true\n")
	if rules != 3 || flush != rules {
		t.Fatalf("%d rules but %d flush_on_shutdown lines:\n%s", rules, flush, out)
	}
}
