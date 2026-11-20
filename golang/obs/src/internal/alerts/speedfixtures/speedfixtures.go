// Package speedfixtures generates the fixture sets of the concept section 11.1.1 speed
// table: 1, 20, 200 and 1,000 alerts as GrafanaAlertRuleGroup CRs (MetricsQL + LogsQL),
// a 4 h and a 24 h seed, and three incidents in the seed format of `obs alerts`.
//
// The 20 base alerts are the prototype's proj20 mix (spike 7), ported from LogQL to
// LogsQL by the rules in the README section "Fixture porting rules". Bigger sets repeat
// the 20 alerts as variants (`v2/`, `v3/` ...): a different rollup window and the same
// threshold (count thresholds scale with the window), so every alert is its own rule.
//
// Everything here is synthetic: the landscape, platform and service names are made up.
package speedfixtures

//go:generate go run ./gen/main.go -out ../../../../tests/fixtures/alerts-speed

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Sizes are the alert counts of the speed table.
var Sizes = []int{1, 20, 200, 1000}

// Lengths are the seed lengths of the speed table.
var Lengths = []string{"4h", "24h"}

// windows are the rollup windows the variants cycle through (variant 1 keeps the base one).
var windows = []string{"5m", "2m", "3m", "4m", "6m", "7m", "8m", "10m", "12m", "15m"}

// query is one data node. Metric and log exprs may hold {W} (the window) and {T} (the
// threshold of the alert, scaled per variant).
type query struct {
	ref  string
	logs bool
	expr string
}

type tmpl struct {
	slug, file, title, sev string
	forD                   string
	queries                []query
	cond                   string  // Grafana math over the queries, {T} / {T2} are thresholds
	t, t2                  float64 // base thresholds
	win                    string  // base window; "" = the expr has no window (kept as is)
	// cnt: which threshold counts events in the window (1 = t, 2 = t2) and so scales with the
	// window length; rate and ratio thresholds do not.
	cnt int
}

const (
	svc  = `{durian.cloud.service="checkout"}`
	evt  = `"durian.cloud/event":=`
	cnt  = ` | stats count() hits`
	m5   = `[{W}]`
	chg  = `checkout_charges_total`
	dur  = `http_server_request_duration_seconds`
	fail = evt + `"checkout.charge.failed"`
)

var base = []tmpl{
	{slug: "charge-errors", file: "critical", title: "Charge errors high", sev: "critical", forD: "10m", t: 1, t2: 10, win: "5m", cnt: 2,
		queries: []query{
			{"A", false, `sum(rate(` + chg + `{outcome="error"}` + m5 + `))`},
			{"B", true, `_time:{W} ` + svc + ` ` + fail + cnt}},
		cond: "$A > {T} && $B > {T2}"},
	{slug: "charge-errors", file: "warning", title: "Charge errors elevated", sev: "warning", forD: "5m", t: 0.5, win: "5m",
		queries: []query{{"A", false, `sum(rate(` + chg + `{outcome="error"}` + m5 + `))`}}, cond: "$A > {T}"},
	{slug: "charge-errors", file: "ratio", title: "Charge error ratio", sev: "warning", forD: "5m", t: 0.1, win: "5m",
		queries: []query{{"A", false, `sum(rate(` + chg + `{outcome="error"}` + m5 + `)) / sum(rate(` + chg + m5 + `))`}}, cond: "$A > {T}"},
	{slug: "charge-errors", file: "increase", title: "Charge errors in the last 10 minutes", sev: "warning", forD: "0s", t: 300, win: "10m", cnt: 1,
		queries: []query{{"A", false, `sum(increase(` + chg + `{outcome="error"}` + m5 + `))`}}, cond: "$A > {T}"},
	{slug: "charge-errors", file: "by-provider", title: "Charge errors by provider", sev: "warning", forD: "5m", t: 0.5, win: "5m",
		queries: []query{{"A", false, `sum by ("payment.provider") (rate(` + chg + `{outcome="error"}` + m5 + `))`}}, cond: "$A > {T}"},
	{slug: "charge-errors", file: "no-window", title: "Charge errors (MetricsQL, no window)", sev: "warning", forD: "5m", t: 1,
		queries: []query{{"A", false, `sum(rate(` + chg + `{outcome="error"}))`}}, cond: "$A > {T}"},
	{slug: "latency", file: "p95-critical", title: "Charge p95 very slow", sev: "critical", forD: "10m", t: 0.8, win: "5m",
		queries: []query{{"A", false, `histogram_quantile(0.95, sum by (le) (rate(` + dur + `_bucket{"http.route"="/charge"}` + m5 + `)))`}}, cond: "$A > {T}"},
	{slug: "latency", file: "p95-warning", title: "Charge p95 slow", sev: "warning", forD: "5m", t: 0.5, win: "5m",
		queries: []query{{"A", false, `histogram_quantile(0.95, sum by (le) (rate(` + dur + `_bucket{"http.route"="/charge"}` + m5 + `)))`}}, cond: "$A > {T}"},
	{slug: "latency", file: "avg", title: "Average request time high", sev: "warning", forD: "5m", t: 0.3, win: "5m",
		queries: []query{{"A", false, `sum(rate(` + dur + `_sum` + m5 + `)) / sum(rate(` + dur + `_count` + m5 + `))`}}, cond: "$A > {T}"},
	{slug: "latency", file: "5xx-ratio", title: "5xx ratio", sev: "warning", forD: "5m", t: 0.05, win: "5m",
		queries: []query{{"A", false, `sum(rate(` + dur + `_count{"http.response.status_code"=~"5.."}` + m5 + `)) / sum(rate(` + dur + `_count` + m5 + `))`}}, cond: "$A > {T}"},
	{slug: "mixed", file: "errors-or-latency", title: "Checkout unhealthy", sev: "critical", forD: "5m", t: 1, t2: 0.5, win: "5m",
		queries: []query{
			{"A", false, `sum(rate(` + chg + `{outcome="error"}` + m5 + `))`},
			{"B", false, `histogram_quantile(0.95, sum by (le) (rate(` + dur + `_bucket` + m5 + `)))`}},
		cond: "$A > {T} || $B > {T2}"},
	{slug: "traffic", file: "low", title: "Checkout traffic dropped", sev: "warning", forD: "10m", t: 1, win: "5m",
		queries: []query{{"A", false, `sum(rate(` + chg + m5 + `))`}}, cond: "$A < {T}"},
	{slug: "metricsql", file: "with-template", title: "Charge errors (MetricsQL WITH)", sev: "warning", forD: "5m", t: 1, win: "5m",
		queries: []query{{"A", false, `WITH (errs = ` + chg + `{outcome="error"}) sum(rate(errs` + m5 + `))`}}, cond: "$A > {T}"},
	{slug: "declines", file: "adyen-metric", title: "Adyen declines (metric)", sev: "warning", forD: "5m", t: 0.5, win: "5m",
		queries: []query{{"A", false, `sum(rate(` + chg + `{"payment.provider"="adyen", outcome="declined"}` + m5 + `))`}}, cond: "$A > {T}"},
	{slug: "declines", file: "adyen-logs", title: "Adyen declines (logs)", sev: "warning", forD: "5m", t: 150, win: "5m", cnt: 1,
		queries: []query{{"A", true, `_time:{W} ` + svc + ` ` + evt + `"checkout.charge.declined" "payment.provider":="adyen"` + cnt}}, cond: "$A > {T}"},
	{slug: "logs", file: "failed-count", title: "Failed charge log lines", sev: "warning", forD: "5m", t: 100, win: "5m", cnt: 1,
		queries: []query{{"A", true, `_time:{W} ` + svc + ` ` + fail + cnt}}, cond: "$A > {T}"},
	{slug: "logs", file: "failed-json", title: "Failed charge log lines (json fields)", sev: "warning", forD: "5m", t: 100, win: "5m", cnt: 1,
		queries: []query{{"A", true, `_time:{W} ` + svc + ` "durian.cloud/event":in("checkout.charge.failed")` + cnt}}, cond: "$A > {T}"},
	{slug: "logs", file: "failed-by-provider", title: "Failed charge log lines by provider", sev: "warning", forD: "5m", t: 100, win: "5m", cnt: 1,
		queries: []query{{"A", true, `_time:{W} ` + svc + ` ` + fail + ` | stats by (payment.provider) count() hits`}}, cond: "$A > {T}"},
	{slug: "logs", file: "error-rate", title: "Error log rate", sev: "warning", forD: "5m", t: 0.5, win: "5m",
		queries: []query{{"A", true, `_time:{W} ` + svc + ` ` + fail + ` severity_text:="ERROR" | stats rate() per_s`}}, cond: "$A > {T}"},
	{slug: "logs", file: "slow-unwrap", title: "Warn log lines high (replaces logged duration)", sev: "warning", forD: "5m", t: 220, win: "5m", cnt: 1,
		queries: []query{{"A", true, `_time:{W} ` + svc + ` ` + evt + `"checkout.charge.declined" severity_text:="WARN"` + cnt}}, cond: "$A > {T}"},
}

// Count is the number of base alerts.
func Count() int { return len(base) }

func secs(w string) int {
	n, _ := strconv.Atoi(strings.TrimSuffix(w, "m"))
	return n * 60
}

// variant returns the window and threshold scale of variant k (1 = the base alert).
func variant(k int) (win string, scale float64) {
	return windows[(k-1)%len(windows)], 1 + 0.1*float64((k-1)/len(windows))
}

func num(v float64) string { return strconv.FormatFloat(v, 'g', 6, 64) }

// render writes one GrafanaAlertRuleGroup CR.
func render(t tmpl, k int) (rel, doc string) {
	win, _ := variant(k)
	scale := 1.0 // thresholds are not scaled: every variant behaves like its base alert
	if t.win == "" {
		win = ""
	}
	t1, t2 := t.t*scale, t.t2*scale
	if win != "" {
		f := float64(secs(win)) / float64(secs(t.win))
		if t.cnt == 1 {
			t1 *= f
		} else if t.cnt == 2 {
			t2 *= f
		}
	}
	repl := strings.NewReplacer("{W}", win, "{T}", num(t1), "{T2}", num(t2))
	slug, id := t.slug, t.slug+"-"+t.file
	if k > 1 {
		slug = fmt.Sprintf("v%d/%s", k, t.slug)
		id = fmt.Sprintf("v%d-%s", k, id)
	}
	rel = fmt.Sprintf("alerts/%s/%s.yaml", slug, t.file)
	rng := 600
	if win != "" {
		rng = secs(win)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "apiVersion: grafana.integreatly.org/v1beta1\nkind: GrafanaAlertRuleGroup\nmetadata:\n  name: speed-checkout-%s\nspec:\n  folderRef: speed-checkout-folder\n  interval: 1m\n  rules:\n", id)
	fmt.Fprintf(&b, "    - uid: %s\n      title: '%s'\n      condition: %s\n      for: %s\n      noDataState: OK\n      execErrState: Error\n      data:\n", id, t.title, condRef(t), t.forD)
	for _, q := range t.queries {
		ds, typ, extra := "speed-metrics", "prometheus", "instant: true"
		if q.logs {
			ds, typ, extra = "speed-logs", "victoriametrics-logs-datasource", "queryType: stats"
		}
		fmt.Fprintf(&b, "        - refId: %s\n          relativeTimeRange: { from: %d, to: 0 }\n          datasourceUid: %s\n          model:\n            datasource: { type: %s, uid: %s }\n            expr: '%s'\n            %s\n", q.ref, rng, ds, typ, ds, repl.Replace(q.expr), extra)
	}
	if len(t.queries) > 1 || strings.Contains(t.cond, "&&") {
		fmt.Fprintf(&b, "        - refId: C\n          datasourceUid: __expr__\n          model: { type: math, expression: '%s' }\n", repl.Replace(t.cond))
	} else {
		fmt.Fprintf(&b, "        - refId: B\n          datasourceUid: __expr__\n          model: { type: math, expression: '%s' }\n", repl.Replace(t.cond))
	}
	fmt.Fprintf(&b, "      labels: { severity: %s }\n      annotations: { summary: '%s' }\n", t.sev, t.title)
	return rel, b.String()
}

func condRef(t tmpl) string {
	if len(t.queries) > 1 {
		return "C"
	}
	return "B"
}

// Alerts returns the files (relative path -> content) of a set of n alerts.
func Alerts(n int) map[string]string {
	out := map[string]string{}
	for i := 0; i < n; i++ {
		rel, doc := render(base[i%len(base)], i/len(base)+1)
		out[rel] = doc
	}
	return out
}

const seedTmpl = `length: LEN
random_seed: 42            # same file -> same data
resource:
  durian.cloud.landscape: speedland
  durian.cloud.platform: payments
  durian.cloud.service: checkout
  durian.cloud.module: api
traffic: { requests: 3/s, shape: daily_wave }
metrics:
  checkout.charges:
    type: counter
    series:
      - { where: { payment.provider: stripe, outcome: success }, share: 0.60 }
      - { where: { payment.provider: stripe, outcome: declined }, share: 0.05 }
      - { where: { payment.provider: stripe, outcome: error }, share: 0.01 }
      - { where: { payment.provider: adyen, outcome: success }, share: 0.30 }
      - { where: { payment.provider: adyen, outcome: declined }, share: 0.03 }
      - { where: { payment.provider: adyen, outcome: error }, share: 0.01 }
  http.server.request.duration:
    type: histogram
    unit: s
    buckets: [0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5]
    latency: { median: 0.08, sigma: 0.5 }
    series:
      - { where: { http.route: /charge, http.response.status_code: "200" }, share: 0.97 }
      - { where: { http.route: /charge, http.response.status_code: "502" }, share: 0.01 }
      - { where: { http.route: /refund, http.response.status_code: "200" }, share: 0.02 }
logs:
  - { event: checkout.charge.succeeded, level: info, from: { metric: checkout.charges, where: { outcome: success } } }
  - { event: checkout.charge.declined, level: warn, from: { metric: checkout.charges, where: { outcome: declined } } }
  - { event: checkout.charge.failed, level: error, from: { metric: checkout.charges, where: { outcome: error } } }
incidents: [stripe-outage, checkout-slow, adyen-declines]
`

type incidentDef struct {
	name, head  string
	fire, quiet []string // base alert ids (rule uid: slug-file)
	within      string
	// disturbance is the reason printed when no alert of a set fires for the incident: it is then a deliberate
	// non-incident disturbance (it proves the alerts stay quiet), which `obs test` leaves out of [incident-uncaught].
	disturbance string
}

// incidentDefs are the three prototype incidents; expect lists are filled per set by Incidents.
var incidentDefs = []incidentDef{
	{"stripe-outage", "story: Stripe returns 502 for 40% of charges for 30 minutes.\nwindow: { start: 90m, duration: 30m }\nchange:\n  - { metric: checkout.charges, where: { payment.provider: stripe, outcome: error }, rate: 1.2/s }\n",
		[]string{"charge-errors-critical", "charge-errors-warning", "charge-errors-ratio", "charge-errors-increase", "charge-errors-by-provider", "charge-errors-no-window", "metricsql-with-template", "mixed-errors-or-latency", "logs-error-rate", "logs-failed-count", "logs-failed-json", "logs-failed-by-provider"}, nil, "30m", ""},
	{"checkout-slow", "story: The charge route gets 6x slower for 40 minutes.\nwindow: { start: 150m, duration: 40m }\nchange:\n  - { metric: http.server.request.duration, where: { http.route: /charge }, latency_x: 6 }\n",
		[]string{"latency-p95-warning", "latency-p95-critical", "latency-avg", "mixed-errors-or-latency"},
		[]string{"charge-errors-critical", "charge-errors-warning", "charge-errors-ratio", "logs-failed-count", "logs-error-rate", "declines-adyen-logs", "traffic-low"}, "30m", "the charge route slows down without any error: proves the error alerts stay quiet"},
	{"adyen-declines", "story: Adyen declines a burst of cards for 20 minutes.\nwindow: { start: 60m, duration: 20m }\nchange:\n  - { metric: checkout.charges, where: { payment.provider: adyen, outcome: declined }, rate: 0.8/s }\n",
		[]string{"declines-adyen-logs", "declines-adyen-metric", "logs-slow-unwrap"},
		[]string{"charge-errors-critical", "charge-errors-ratio", "latency-p95-warning", "traffic-low"}, "30m", "declined cards are not failures: proves the error alerts stay quiet"},
}

// Incidents returns the incident files of a set of n alerts, naming alerts by rule uid. Every repeated alert (v2-...)
// is named in expect.fire or expect.quiet like its base alert: a variant inherits the
// base alert's expectation. Base alerts the prototype listed in neither stay unlisted.
func Incidents(n int) map[string]string {
	out := map[string]string{}
	for _, d := range incidentDefs {
		var fire, quiet []string
		for k := 1; k <= (n+len(base)-1)/len(base); k++ {
			pre := ""
			if k > 1 {
				pre = fmt.Sprintf("v%d-", k)
			}
			for i := 0; i < len(base) && (k-1)*len(base)+i < n; i++ {
				id := base[i].slug + "-" + base[i].file
				if contains(d.fire, id) {
					fire = append(fire, pre+id)
				} else {
					quiet = append(quiet, pre+id)
				}
			}
		}
		out[d.name] = fmt.Sprintf("%sexpect:\n  fire: [%s]\n  quiet: [%s]\n  fire_within: %s\n  resolve_within: 20m\n", d.head, strings.Join(fire, ", "), strings.Join(quiet, ", "), d.within) + d.marker(fire)
	}
	return out
}

// marker is the disturbance line of an incident no alert of its set is expected to fire for ("" otherwise).
func (d incidentDef) marker(fire []string) string {
	if len(fire) > 0 || d.disturbance == "" {
		return ""
	}
	return fmt.Sprintf("disturbance: '%s'\n", d.disturbance)
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// Seed returns the seed file of a given length.
func Seed(length string) string { return strings.Replace(seedTmpl, "LEN", length, 1) }

// Write writes the whole fixture tree under out: n<N>/ and the trace sets t<N>/ (alerts +
// incidents + seed.4h.yaml + seed.24h.yaml). The runner copies one set and one seed to <dir>/seed.yaml per run.
func Write(out string) error {
	for _, n := range Sizes {
		root := filepath.Join(out, fmt.Sprintf("n%d", n))
		if err := os.RemoveAll(root); err != nil {
			return err
		}
		files := Alerts(n)
		for name, body := range Incidents(n) {
			files["incidents/"+name+".yaml"] = body
		}
		files["contract.yaml"] = contract
		files["obs.yaml"] = obsYAML
		for _, l := range Lengths {
			files["seed."+l+".yaml"] = Seed(l)
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
	return writeTraceSets(out)
}

// contract is the checkout contract every set carries (it names every metric label and log
// event the seed and the alerts use).
const contract = `service: checkout

types:
  provider: { kind: enum, values: [stripe, adyen] }
  outcome: { kind: enum, values: [success, declined, error] }

metrics:
  checkout.charges:
    kind: counter
    budget: 20
    labels: { payment.provider: provider, outcome: outcome }

logs:
  ChargeSucceeded: { event: checkout.charge.succeeded, level: info }
  ChargeDeclined: { event: checkout.charge.declined, level: warn }
  ChargeFailed: { event: checkout.charge.failed, level: error }

platform_metrics:
  http.server.request.duration: [http.route, http.response.status_code]
`

// obsYAML is the obs.yaml every set carries: the versions this obs embeds and the default thresholds.
const obsYAML = `# The versions and thresholds this repo's observability folder targets. obs reads this file
# (obs check, obs query, obs alerts, obs sandbox up, obs look); see the obs README.
# Every key is optional: a key that is absent keeps the version or default built into obs.
versions:
  grafana: 13.3.0 # alert rules and dashboards; the alert engine and dashboard schema are checked
  victoriametrics: v1.156.0 # MetricsQL
  victorialogs: v1.54.0 # LogsQL
  tempo: 3.1.1 # TraceQL
  alloy: v1.21.3 # reference only; no evaluator
  pyroscope: 2.4.3 # sandbox image
  dashboard_schema: 42 # Grafana 13.3.0 dashboard schemaVersion

thresholds:
  logs: # VictoriaLogs query limits (the -search.* flags); victorialogs-settings.yaml overrides
    max_query_len: 16KiB
    max_query_time_range: 0
    max_query_duration: 30s
    max_queue_duration: 10s
  metrics:
    max_points_per_series: 1000000
  traces: # Tempo query_frontend; --tempo-config overrides
    query_end_cutoff: 30s
    metrics_max_duration: 24h
    metrics_max_intervals: 10000
  profiles: # obs profile compare, as a fraction (0.05 is 5 %)
    tolerance_function: 0.05
    tolerance_span: 0.05
    tolerance_total: 0.15
    tolerance_dotnet_trace: 0.30
`
