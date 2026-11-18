package contract

import (
	"strconv"
	"strings"
	"testing"
)

// A cluster contract in the shape of the platform draft: every metric has a source, labels
// that follow another label add no series, and estimate types give a size range.
const sourceDoc = `service: k8s-monitoring
types:
  pod: { kind: estimate, min: 28, max: 110, churn_per_day: 25 }
  node: { kind: estimate, min: 1, max: 2, churn_per_day: 0 }
  pod_phase: { kind: enum, values: [Pending, Running, Succeeded, Failed, Unknown] }
metrics:
  kube_pod_info:
    source: kube-state-metrics
    kind: gauge
    budget: 110
    used_by: [box/dash/pods]
    labels:
      pod: pod
      uid: { follows: pod }
      namespace: { follows: pod }
      node: { follows: pod }
  kube_pod_status_phase:
    source: kube-state-metrics
    kind: gauge
    budget: 550
    labels:
      pod: pod
      uid: { follows: pod }
      phase: pod_phase
  kube_pod_container_status_restarts_total:
    source: kube-state-metrics
    kind: counter
    budget: 110
    labels:
      pod: pod
      container: { follows: pod }
`

func TestSourceEntriesAreValid(t *testing.T) {
	c := parse(t, sourceDoc)
	if is := c.Validate(); len(is) > 0 {
		t.Fatalf("source contract refused: %v", is)
	}
	m := c.Metrics["kube_pod_info"]
	if m.Source != "kube-state-metrics" || len(m.UsedBy) != 1 || m.UsedBy[0] != "box/dash/pods" {
		t.Fatalf("source/used_by not parsed: %+v", m)
	}
	if got := strings.Join(m.LabelOrder, ","); got != "pod,uid,namespace,node" {
		t.Fatalf("label order %s", got)
	}
	if m.Follows["uid"] != "pod" || m.Labels["pod"] != "pod" {
		t.Fatalf("labels %v follows %v", m.Labels, m.Follows)
	}
}

// an estimate counts its max, a follows label adds nothing
func TestWorstCaseUsesEstimateMaxAndSkipsFollows(t *testing.T) {
	c := parse(t, sourceDoc)
	for name, want := range map[string]int64{"kube_pod_info": 110, "kube_pod_status_phase": 550, "kube_pod_container_status_restarts_total": 110} {
		if w, ok := c.WorstCase(name); !ok || w != want {
			t.Errorf("%s: worst case %d %v, want %d", name, w, ok, want)
		}
	}
}

func TestSourceOverBudgetUsesMax(t *testing.T) {
	c := parse(t, strings.Replace(sourceDoc, "budget: 110\n    used_by", "budget: 109\n    used_by", 1))
	if !hasRule(c.Validate(), "over-budget", "kube_pod_info") {
		t.Fatalf("a budget under the estimate max must fail: %v", c.Validate())
	}
}

func TestChurnPerDay(t *testing.T) {
	c := parse(t, sourceDoc)
	// pod churns 25 new values a day; each is one series in kube_pod_info, five (one per phase) in the phase metric
	if got := c.ChurnPerDay("kube_pod_info"); got != 25 {
		t.Errorf("kube_pod_info churn %d, want 25", got)
	}
	if got := c.ChurnPerDay("kube_pod_status_phase"); got != 125 {
		t.Errorf("kube_pod_status_phase churn %d, want 125", got)
	}
}

// estimate is for entries somebody else makes: the file and line of the label are named
func TestEstimateOnlyOnSource(t *testing.T) {
	doc := strings.Replace(sourceDoc, "    source: kube-state-metrics\n    kind: gauge\n    budget: 110\n", "    kind: gauge\n    across_pods: max\n    budget: 110\n", 1)
	c := parse(t, doc)
	var found *Issue
	for _, i := range c.Validate() {
		if i.Rule == "estimate-not-source" {
			i := i
			found = &i
		}
	}
	if found == nil {
		t.Fatalf("estimate on an own metric accepted: %v", c.Validate())
	}
	line := lineOf(sourceDoc, "kube_pod_info:") // the label sits a few lines below the metric
	if !strings.Contains(found.Msg, "c.yaml:") || !strings.Contains(found.Msg, `"pod"`) || !strings.Contains(found.Msg, "kube_pod_info") || line == 0 {
		t.Fatalf("message must name file:line, the label and the metric: %s", found.Msg)
	}
	if !strings.Contains(found.Msg, "c.yaml:"+strconv.Itoa(lineOf(doc, "      pod: pod"))) {
		t.Fatalf("wrong line in %q (want line %d)", found.Msg, lineOf(doc, "      pod: pod"))
	}
}

func TestFollowsOnlyOnSourceAndMustResolve(t *testing.T) {
	own := `service: s
types:
  a: { kind: enum, values: [x, y] }
metrics:
  m:
    kind: counter
    budget: 2
    labels:
      a: a
      b: { follows: a }
`
	if !hasRule(parse(t, own).Validate(), "follows-not-source", `"b"`) {
		t.Errorf("follows on an own metric accepted: %v", parse(t, own).Validate())
	}
	src := strings.Replace(own, "    kind: counter", "    source: ksm\n    kind: counter", 1)
	if is := parse(t, src).Validate(); len(is) != 0 {
		t.Errorf("follows on a source entry refused: %v", is)
	}
	missing := strings.Replace(src, "follows: a", "follows: nope", 1)
	if !hasRule(parse(t, missing).Validate(), "follows", "nope") {
		t.Errorf("follows to a missing label accepted: %v", parse(t, missing).Validate())
	}
	loop := strings.Replace(src, "      a: a\n      b: { follows: a }", "      a: { follows: b }\n      b: { follows: a }", 1)
	if !hasRule(parse(t, loop).Validate(), "follows", "cycle") {
		t.Errorf("follows cycle accepted: %v", parse(t, loop).Validate())
	}
}

func TestEstimateTypeNeedsAllThreeNumbers(t *testing.T) {
	for _, bad := range []string{
		"{ kind: estimate, min: 1, max: 5 }",
		"{ kind: estimate, max: 5, churn_per_day: 1 }",
		"{ kind: estimate, min: 9, max: 5, churn_per_day: 1 }",
		"{ kind: estimate, min: 0, max: 0, churn_per_day: 0 }",
		"{ kind: estimate, min: 0, max: 5, churn_per_day: -1 }",
	} {
		c := parse(t, "service: s\ntypes:\n  p: "+bad+"\n")
		if !hasRule(c.Validate(), "estimate-bounds", `"p"`) {
			t.Errorf("%s accepted: %v", bad, c.Validate())
		}
	}
}

// an estimate is never a span attribute or a stream label either
func TestEstimateNotOnSpans(t *testing.T) {
	doc := "service: s\ntypes:\n  p: { kind: estimate, min: 1, max: 5, churn_per_day: 0 }\nspans:\n  a.b:\n    attributes: { x: p }\n"
	if !hasRule(parse(t, doc).Validate(), "estimate-not-source", "x") {
		t.Fatalf("estimate on a span attribute accepted: %v", parse(t, doc).Validate())
	}
}

func TestFollowsUnknownKeyFails(t *testing.T) {
	doc := "service: s\nmetrics:\n  m:\n    source: x\n    kind: gauge\n    labels:\n      a: { follow: b }\n"
	if _, err := Parse("c.yaml", []byte(doc)); err == nil || !strings.Contains(err.Error(), "follow") {
		t.Fatalf("misspelt key accepted: %v", err)
	}
	doc = "service: s\nmetrics:\n  m:\n    sauce: x\n    kind: gauge\n"
	if _, err := Parse("c.yaml", []byte(doc)); err == nil {
		t.Fatal("unknown metric key accepted")
	}
}

// the gauge pod rule is for our own metrics; a scraped gauge has pods and says nothing of them
func TestSourceGaugeNeedsNoAcrossPods(t *testing.T) {
	c := parse(t, sourceDoc)
	if hasRule(c.Validate(), "gauge-pods", "") {
		t.Fatal("source gauge asked for across_pods")
	}
	own := strings.Replace(sourceDoc, "    source: kube-state-metrics\n    kind: gauge\n    budget: 110\n    used_by: [box/dash/pods]\n    labels:\n      pod: pod\n      uid: { follows: pod }\n      namespace: { follows: pod }\n      node: { follows: pod }", "    kind: gauge\n    budget: 3\n    labels:\n      phase: pod_phase", 1)
	if !hasRule(parse(t, own).Validate(), "gauge-pods", "kube_pod_info") {
		t.Fatal("own gauge without across_pods must still fail")
	}
}

func TestUsedByWarnsOnSourceOnly(t *testing.T) {
	c := parse(t, sourceDoc)
	w := c.Warnings()
	var names []string
	for _, i := range w {
		if i.Rule != "used-by-missing" {
			t.Fatalf("unexpected warning %v", i)
		}
		names = append(names, i.Path)
	}
	if strings.Join(names, ",") != "metrics.kube_pod_status_phase,metrics.kube_pod_container_status_restarts_total" {
		t.Fatalf("warnings %v", names)
	}
	dup := strings.Replace(sourceDoc, "used_by: [box/dash/pods]", "used_by: [a, a]", 1)
	if !hasRule(parse(t, dup).Validate(), "used-by", `"a"`) {
		t.Fatalf("duplicate used_by accepted: %v", parse(t, dup).Validate())
	}
}

// a source name is already the stored name: no _total is added; a histogram keeps its three parts
func TestStoredNamesOfSourceEntries(t *testing.T) {
	if got := StoredNames("node_boot_time", Metric{Source: "node-exporter", Kind: MetricCounter}); strings.Join(got, ",") != "node_boot_time" {
		t.Errorf("source counter %v", got)
	}
	if got := StoredNames("etcd_request_duration_seconds", Metric{Source: "etcd", Kind: MetricHistogram}); strings.Join(got, ",") != "etcd_request_duration_seconds_bucket,etcd_request_duration_seconds_sum,etcd_request_duration_seconds_count" {
		t.Errorf("source histogram %v", got)
	}
	if got := StoredNames("checkout.charges", Metric{Kind: MetricCounter}); got[0] != "checkout_charges_total" {
		t.Errorf("own counter %v", got)
	}
}

func TestOwnDropsSourceEntriesAndEstimateTypes(t *testing.T) {
	c := parse(t, sourceDoc)
	o := c.Own()
	if len(o.MetricOrder) != 0 || len(o.Metrics) != 0 {
		t.Fatalf("own keeps source metrics: %v", o.MetricOrder)
	}
	for _, n := range o.TypeOrder {
		if o.Types[n].Kind == KindEstimate {
			t.Fatalf("own keeps estimate type %s", n)
		}
	}
	if o.Hash != c.Hash {
		t.Fatal("own must keep the contract hash")
	}
	if len(c.MetricOrder) != 3 {
		t.Fatal("Own changed the original")
	}
}

func TestKeepNames(t *testing.T) {
	doc := sourceDoc + `  checkout.charges:
    kind: counter
    budget: 2
    labels: { phase: pod_phase }
  checkout.charge.duration:
    kind: histogram
    unit: seconds
platform_metrics:
  http.server.request.duration: [http.route]
  http.server.active_requests: [http.route]
`
	c := parse(t, doc)
	got := c.KeepNames()
	for _, want := range []string{
		"kube_pod_info", "kube_pod_status_phase", "kube_pod_container_status_restarts_total",
		"checkout_charges_total", "checkout_charge_duration_seconds_bucket", "checkout_charge_duration_seconds_sum", "checkout_charge_duration_seconds_count",
		"http_server_request_duration_seconds_bucket", "http_server_request_duration_seconds_sum", "http_server_request_duration_seconds_count",
	} {
		if !in(got, want) {
			t.Errorf("keep list lacks %s: %v", want, got)
		}
	}
	for i := 1; i < len(got); i++ {
		if got[i-1] >= got[i] {
			t.Fatalf("keep list must be sorted and unique: %v", got)
		}
	}
	// no regex, no dots
	for _, n := range got {
		if strings.ContainsAny(n, ".*|()[]\\^$+?{}") {
			t.Errorf("keep name %q is not a plain name", n)
		}
	}
}

func lineOf(doc, needle string) int {
	for i, l := range strings.Split(doc, "\n") {
		if strings.Contains(l, needle) {
			return i + 1
		}
	}
	return 0
}

// A scraped label can really be empty: only a type that an own metric uses must name every value.
func TestEmptyEnumValueOnlyForSourceTypes(t *testing.T) {
	src := "service: s\ntypes:\n  prio: { kind: enum, values: ['', high] }\nmetrics:\n  m:\n    source: ksm\n    kind: gauge\n    budget: 2\n    labels: { prio: prio }\n"
	if is := parse(t, src).Validate(); len(is) != 0 {
		t.Fatalf("empty value on a source-only type refused: %v", is)
	}
	own := strings.Replace(src, "    source: ksm\n    kind: gauge\n", "    kind: counter\n", 1)
	if !hasRule(parse(t, own).Validate(), "enum-value", "prio") {
		t.Fatalf("empty value on an own type accepted: %v", parse(t, own).Validate())
	}
}

// A type only a source: entry uses is not generated: its empty value (” as in a pod with no priority class)
// would become a name in code, and nobody asked for the type.
func TestOwnDropsSourceOnlyTypesKeepsSharedOnes(t *testing.T) {
	doc := `service: s
types:
  prio: { kind: enum, values: ['', high] }
  lane: { kind: enum, values: [a, b] }
  shared: { kind: enum, values: [x, y] }
  unused: { kind: enum, values: [u, v] }
metrics:
  scraped:
    source: ksm
    kind: gauge
    budget: 8
    labels: { prio: prio, lane: lane, shared: shared }
  mine:
    kind: counter
    budget: 2
    labels: { lane: lane }
spans:
  mine.span:
    attributes: { shared: shared }
`
	c := parse(t, doc)
	if is := c.Validate(); len(is) != 0 {
		t.Fatalf("valid contract refused: %v", is)
	}
	o := c.Own()
	if _, ok := o.Types["prio"]; ok {
		t.Fatalf("own keeps the source-only type prio: %v", o.TypeOrder)
	}
	for _, keep := range []string{"lane", "shared", "unused"} {
		if _, ok := o.Types[keep]; !ok {
			t.Errorf("own lost type %s: %v", keep, o.TypeOrder)
		}
	}
	for _, n := range o.TypeOrder {
		if n == "prio" {
			t.Fatalf("TypeOrder keeps prio: %v", o.TypeOrder)
		}
	}
	if _, ok := c.Types["prio"]; !ok {
		t.Fatal("Own changed the original")
	}
	// an unused type may not list an empty value: it is generated
	un := strings.Replace(doc, "unused: { kind: enum, values: [u, v] }", "unused: { kind: enum, values: ['', v] }", 1)
	if !hasRule(parse(t, un).Validate(), "enum-value", "unused") {
		t.Fatalf("empty value on an unused type accepted: %v", parse(t, un).Validate())
	}
	// a type a source: entry and an own metric both use may not list one
	both := strings.Replace(doc, "values: [x, y]", "values: ['', y]", 1)
	if !hasRule(parse(t, both).Validate(), "enum-value", "shared") {
		t.Fatalf("empty value on a shared type accepted: %v", parse(t, both).Validate())
	}
}
