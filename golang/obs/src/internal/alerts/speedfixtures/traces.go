package speedfixtures

// Trace sets t1 and t20 of the speed table: TraceQL alert rules only (Tempo
// datasource), on the same seed plus two trace definitions, and the same three
// incidents. Each seeded request event of a trace source makes one trace: about 86,000
// traces (130,000 spans) per run on the 4 h seed, six times that on the 24 h seed.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// TraceSizes are the trace-rule counts of the speed table.
var TraceSizes = []int{1, 20}

type traceTmpl struct {
	id, title, sev, forD string
	instant              bool
	from                 int    // relativeTimeRange.from in seconds
	step                 string // range queries: model.step ("" = Tempo's default)
	queries              []string
	reduce               string // reducer of each query
	cond                 string // threshold over B (one query) or Grafana math over the reduced refs
	fire                 []string
}

const (
	charge  = `span.http.route = "/charge" && span.http.response.status_code = 200`
	noExemp = ` with (exemplars=false)`
)

// traceBase: 20 rules. fire names the incidents each one fires in; it is quiet
// everywhere else (written from the seed rates, then confirmed by the evaluator; README).
var traceBase = []traceTmpl{
	{id: "errors-rate", title: "Error spans", sev: "critical", forD: "5m", instant: true, from: 300,
		queries: []string{`{ status = error } | rate()`}, reduce: "last", cond: "gt 0.5", fire: []string{"stripe-outage"}},
	{id: "errors-by-provider", title: "Error spans by provider", sev: "warning", forD: "5m", instant: true, from: 300,
		queries: []string{`{ status = error && span.outcome = "error" } | rate() by (span.payment.provider)`}, reduce: "last", cond: "gt 0.5", fire: []string{"stripe-outage"}},
	{id: "errors-count", title: "Error spans in 5 minutes", sev: "warning", forD: "0s", instant: true, from: 300,
		queries: []string{`{ status = error } | count_over_time()`}, reduce: "last", cond: "gt 150", fire: []string{"stripe-outage"}},
	{id: "errors-ratio", title: "Error span ratio", sev: "warning", forD: "5m", instant: true, from: 300,
		queries: []string{`{ status = error } | rate()`, `{ kind = server } | rate()`}, reduce: "last", cond: "$B / $D > 0.1", fire: []string{"stripe-outage"}},
	{id: "errors-range", title: "Error spans per minute (range)", sev: "warning", forD: "5m", from: 600, step: "1m",
		queries: []string{`{ status = error } | count_over_time()` + noExemp}, reduce: "max", cond: "gt 40", fire: []string{"stripe-outage"}},
	{id: "client-errors", title: "Failing provider calls", sev: "warning", forD: "5m", instant: true, from: 300,
		queries: []string{`{ kind = client && status = error } | rate()`}, reduce: "last", cond: "gt 0.5", fire: []string{"stripe-outage"}},
	{id: "errors-structural", title: "Server spans with a failing provider call", sev: "warning", forD: "5m", instant: true, from: 300,
		queries: []string{`{ kind = server } > { kind = client && status = error } | rate()`}, reduce: "last", cond: "gt 0.5", fire: []string{"stripe-outage"}},
	{id: "errors-by-service", title: "Error spans by service", sev: "warning", forD: "5m", instant: true, from: 300,
		queries: []string{`{ status = error } | rate() by (resource.service.name)`}, reduce: "last", cond: "gt 0.5", fire: []string{"stripe-outage"}},
	{id: "latency-p95", title: "Charge p95 slow", sev: "warning", forD: "5m", instant: true, from: 300,
		queries: []string{`{ ` + charge + ` } | quantile_over_time(duration, .95)`}, reduce: "last", cond: "gt 0.5", fire: []string{"checkout-slow"}},
	{id: "latency-p50", title: "Charge median slow", sev: "warning", forD: "5m", instant: true, from: 300,
		queries: []string{`{ ` + charge + ` } | quantile_over_time(duration, .5)`}, reduce: "last", cond: "gt 0.3", fire: []string{"checkout-slow"}},
	{id: "latency-p99", title: "Charge p99 very slow", sev: "critical", forD: "10m", instant: true, from: 300,
		queries: []string{`{ ` + charge + ` } | quantile_over_time(duration, .99)`}, reduce: "last", cond: "gt 1", fire: []string{"checkout-slow"}},
	{id: "latency-avg", title: "Charge average time high", sev: "warning", forD: "5m", instant: true, from: 300,
		queries: []string{`{ ` + charge + ` } | avg_over_time(duration)`}, reduce: "last", cond: "gt 0.3", fire: []string{"checkout-slow"}},
	{id: "latency-max", title: "Slowest charge", sev: "warning", forD: "5m", instant: true, from: 300,
		queries: []string{`{ ` + charge + ` } | max_over_time(duration)`}, reduce: "last", cond: "gt 1.2", fire: []string{"checkout-slow"}},
	{id: "latency-sum", title: "Charge time spent", sev: "warning", forD: "5m", instant: true, from: 300,
		queries: []string{`{ ` + charge + ` } | sum_over_time(duration)`}, reduce: "last", cond: "gt 250", fire: []string{"checkout-slow"}},
	{id: "latency-range", title: "Charge p95 slow (range)", sev: "warning", forD: "5m", from: 600, step: "1m",
		queries: []string{`{ ` + charge + ` } | quantile_over_time(duration, .95)` + noExemp}, reduce: "mean", cond: "gt 0.5", fire: []string{"checkout-slow"}},
	{id: "declines-adyen", title: "Adyen declines", sev: "warning", forD: "5m", instant: true, from: 300,
		queries: []string{`{ span.payment.provider = "adyen" && span.outcome = "declined" } | rate()`}, reduce: "last", cond: "gt 0.5", fire: []string{"adyen-declines"}},
	{id: "declines-by-provider", title: "Declines by provider", sev: "warning", forD: "5m", instant: true, from: 300,
		queries: []string{`{ span.outcome = "declined" } | count_over_time() by (span.payment.provider)`}, reduce: "last", cond: "gt 150", fire: []string{"adyen-declines"}},
	{id: "traffic-low", title: "Checkout traffic dropped", sev: "warning", forD: "10m", instant: true, from: 300,
		queries: []string{`{ kind = server } | rate()`}, reduce: "last", cond: "lt 1"},
	{id: "refund-errors", title: "Refund errors", sev: "warning", forD: "5m", instant: true, from: 300,
		queries: []string{`{ span.http.route = "/refund" && status = error } | rate()`}, reduce: "last", cond: "gt 0.1"},
	{id: "slow-provider-calls", title: "Slow provider calls (range, default step)", sev: "warning", forD: "5m", from: 300,
		queries: []string{`{ kind = client } | quantile_over_time(duration, .9)` + noExemp}, reduce: "max", cond: "gt 2"},
}

// renderTrace writes one GrafanaAlertRuleGroup CR with Tempo queries: each query A
// (and C) is reduced to B (and D); E is the condition.
func renderTrace(t traceTmpl) (rel, doc string) {
	id := "traces-" + t.id
	rel = fmt.Sprintf("alerts/traces/%s.yaml", t.id)
	var b strings.Builder
	fmt.Fprintf(&b, "apiVersion: grafana.integreatly.org/v1beta1\nkind: GrafanaAlertRuleGroup\nmetadata:\n  name: speed-checkout-%s\nspec:\n  folderRef: speed-checkout-folder\n  interval: 1m\n  rules:\n", id)
	fmt.Fprintf(&b, "    - uid: %s\n      title: '%s'\n      condition: E\n      for: %s\n      noDataState: OK\n      execErrState: Error\n      data:\n", id, t.title, t.forD)
	mode := "metricsQueryType: range"
	if t.instant {
		mode = "metricsQueryType: instant"
	}
	if t.step != "" {
		mode += "\n            step: " + t.step
	}
	for i, q := range t.queries {
		ref, red := []string{"A", "C"}[i], []string{"B", "D"}[i]
		fmt.Fprintf(&b, "        - refId: %s\n          queryType: traceql\n          relativeTimeRange: { from: %d, to: 0 }\n          datasourceUid: speed-traces\n          model:\n            datasource: { type: tempo, uid: speed-traces }\n            queryType: traceql\n            %s\n            query: '%s'\n", ref, t.from, mode, q)
		fmt.Fprintf(&b, "        - refId: %s\n          datasourceUid: __expr__\n          model: { type: reduce, expression: %s, reducer: %s, settings: { mode: dropNN } }\n", red, ref, t.reduce)
	}
	if len(t.queries) == 1 {
		op, val, _ := strings.Cut(t.cond, " ")
		fmt.Fprintf(&b, "        - refId: E\n          datasourceUid: __expr__\n          model: { type: threshold, expression: B, conditions: [{ evaluator: { type: %s, params: [%s] } }] }\n", op, val)
	} else {
		fmt.Fprintf(&b, "        - refId: E\n          datasourceUid: __expr__\n          model: { type: math, expression: '%s' }\n", t.cond)
	}
	fmt.Fprintf(&b, "      labels: { severity: %s }\n      annotations: { summary: '%s' }\n", t.sev, t.title)
	return rel, b.String()
}

// TraceAlerts returns the files of a trace set of n rules (n <= 20).
func TraceAlerts(n int) map[string]string {
	out := map[string]string{}
	for i := 0; i < n && i < len(traceBase); i++ {
		rel, doc := renderTrace(traceBase[i])
		out[rel] = doc
	}
	return out
}

// TraceIncidents returns the incident files of a trace set: every rule is listed in
// expect.fire or expect.quiet of every incident.
func TraceIncidents(n int) map[string]string {
	out := map[string]string{}
	for _, d := range incidentDefs {
		var fire, quiet []string
		for i := 0; i < n && i < len(traceBase); i++ {
			id := "traces-" + traceBase[i].id
			if contains(traceBase[i].fire, d.name) {
				fire = append(fire, id)
			} else {
				quiet = append(quiet, id)
			}
		}
		out[d.name] = fmt.Sprintf("%sexpect:\n  fire: [%s]\n  quiet: [%s]\n  fire_within: %s\n  resolve_within: 20m\n", d.head, strings.Join(fire, ", "), strings.Join(quiet, ", "), d.within) + d.marker(fire)
	}
	return out
}

// traceDefs: one trace per charge (server span and a provider client call) and one
// per HTTP request (server span; 502 is an error).
const traceDefs = `traces:
  - { from: { metric: checkout.charges }, server: charge, client: "{payment.provider}.charge", error_when: { outcome: error }, median_s: 0.12 }
  - { from: { metric: http.server.request.duration }, server: HTTP POST, error_when: { http.response.status_code: "502" }, median_s: 0.08 }
`

// TraceSeed is Seed with the trace definitions.
func TraceSeed(length string) string {
	return strings.Replace(Seed(length), "incidents: [", traceDefs+"incidents: [", 1)
}

// traceContract adds the span attributes the trace rules read.
const traceContract = contract + `
spans:
  checkout.charge:
    attributes: { payment.provider: provider, outcome: outcome }
  http.server:
    attributes: { http.route: text, http.response.status_code: text }
`

func writeTraceSets(out string) error {
	for _, n := range TraceSizes {
		root := filepath.Join(out, fmt.Sprintf("t%d", n))
		if err := os.RemoveAll(root); err != nil {
			return err
		}
		files := TraceAlerts(n)
		for name, body := range TraceIncidents(n) {
			files["incidents/"+name+".yaml"] = body
		}
		files["contract.yaml"] = strings.Replace(traceContract, "types:\n", "types:\n  text: { kind: free }\n", 1)
		files["obs.yaml"] = obsYAML
		for _, l := range Lengths {
			files["seed."+l+".yaml"] = TraceSeed(l)
		}
		for rel, body := range files {
			p := filepath.Join(root, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}
