package files

import (
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// shippingCR is the rendering shipping.md ("Example") gives for the retry-queue critical.yaml of
// alerts.md, with serviceTree {landscape: raichu, platform: tracker, service: zinc, module: webhook}.
const shippingCR = `apiVersion: grafana.integreatly.org/v1beta1
kind: GrafanaAlertRuleGroup
metadata:
  name: tracker-zinc-retry-queue-critical
  namespace: raichu-grafana
spec:
  instanceSelector:
    matchLabels: { dashboards: raichu }
  folderRef: tracker-zinc-folder
  interval: 1m
  rules:
    - uid: tracker-zinc-retry-queue-critical
      title: 🚨 Retry queue saturated
      condition: B
      for: 10m
      noDataState: OK
      execErrState: Error
      data:
        - refId: A
          relativeTimeRange: { from: 600, to: 0 }
          datasourceUid: raichu-metrics
          model: { expr: 'webhook_retry_queue_depth{"durian.cloud.service"="zinc"} > 5000', instant: true }
        - refId: B
          datasourceUid: __expr__
          model: { type: math, expression: 'is_number($A) || is_nan($A) || is_inf($A)' }
      labels:
        severity: critical
        landscape: raichu
        platform: tracker
        service: zinc
        module: webhook
      annotations:
        summary: 'Retry queue at {{ $values.A.Value }}; deliveries dropping'
        description: 'Check downstream health; the queue drains only if endpoints are up.'
        runbook_url: https://github.com/example/zinc/blob/main/observability/alerts/retry-queue/runbook.md
        __dashboardUid__: tracker-zinc-overview
        __panelId__: '2'
`

// alerts.md "The tier file" example.
const retryQueueFlat = `title: Retry queue saturated
datasource: metrics
expr: webhook_retry_queue_depth{"durian.cloud.service"="zinc"} > 5000
for: 10m
summary: 'Retry queue at {{ $values.A.Value }}; deliveries dropping'
description: 'Check downstream health; the queue drains only if endpoints are up.'
panel: 2
`

const flatObs = "platform: tracker\n"
const flatContract = "service: zinc\n"

func flatDir(t *testing.T, rel, body string) *Set {
	t.Helper()
	dir := t.TempDir()
	write(t, dir, "obs.yaml", flatObs)
	write(t, dir, "contract.yaml", flatContract)
	write(t, dir, rel, body)
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// The flat file builds the rule shipping.md says the chart renders. Only what obs cannot know is
// absent: the landscape and module labels, runbook_url, and the landscape's datasource uid.
func TestFlatTierBuildsTheRuleTheChartRenders(t *testing.T) {
	flat := flatDir(t, "alerts/retry-queue/critical.yaml", retryQueueFlat)
	if len(flat.Problems) != 0 || len(flat.Rules) != 1 {
		t.Fatalf("problems %v rules %d", flat.Problems, len(flat.Rules))
	}
	dir := t.TempDir()
	write(t, dir, "alerts/retry-queue/critical.yaml", shippingCR)
	cr, err := Load(dir)
	if err != nil || len(cr.Problems) != 0 || len(cr.Rules) != 1 {
		t.Fatalf("%v %v", err, cr.Problems)
	}
	want := cr.Rules[0]
	want.File = "alerts/retry-queue/critical.yaml"
	want.Flat = true
	delete(want.Definition.Labels, "landscape")
	delete(want.Definition.Labels, "module")
	delete(want.Definition.Annotations, "runbook_url")
	want.Definition.Data[0].DatasourceUID = "metrics"
	got := flat.Rules[0]
	if !reflect.DeepEqual(got, want) {
		g, _ := yaml.Marshal(got)
		w, _ := yaml.Marshal(want)
		t.Fatalf("flat rule differs from the chart's:\n got %s\nwant %s", g, w)
	}
	// and the queries the checker sees: A only (B is a Grafana expression)
	if len(flat.Queries) != 1 || flat.Queries[0].Kind != Metrics || flat.Queries[0].Expr != `webhook_retry_queue_depth{"durian.cloud.service"="zinc"} > 5000` ||
		flat.Queries[0].RangeSecs != 600 || flat.Queries[0].ToSecs != 0 || flat.Queries[0].Where != "rule tracker-zinc-retry-queue-critical query A" {
		t.Fatalf("%+v", flat.Queries)
	}
}

func TestFlatLogsTierIsAStatsQuery(t *testing.T) {
	s := flatDir(t, "alerts/disk/warning.yaml", `title: Disk corrupted
datasource: logs
expr: '_stream:{"durian.cloud.service"="zinc"} "durian.cloud/event":=disk.corrupted | stats count() as hits | filter hits:>0'
for: 0m
summary: s
description: d
`)
	if len(s.Problems) != 0 || len(s.Rules) != 1 {
		t.Fatalf("%v", s.Problems)
	}
	r := s.Rules[0].Definition
	a := r.Data[0]
	if r.UID != "tracker-zinc-disk-warning" || r.Title != "⚠️ Disk corrupted" || r.Labels["severity"] != "warning" || a.DatasourceUID != "logs" ||
		a.Model["queryType"] != "stats" || a.Model["instant"] != nil || a.RelativeTimeRange.From != 600 || r.Condition != "B" || r.NoDataState != "OK" || r.ExecErrState != "Error" || r.For != "0m" {
		t.Fatalf("%+v", r)
	}
	if _, ok := r.Annotations["__panelId__"]; ok {
		t.Fatalf("no panel set: no panel annotation: %v", r.Annotations)
	}
	if s.Queries[0].Kind != Logs || s.Queries[0].QueryType != "stats" {
		t.Fatalf("%+v", s.Queries[0])
	}
}

func TestFlatSeverityPrefixes(t *testing.T) {
	for sev, prefix := range map[string]string{"critical": "🚨 ", "warning": "⚠️ ", "info": "ℹ️ "} {
		s := flatDir(t, "alerts/q/"+sev+".yaml", retryQueueFlat)
		if len(s.Rules) != 1 || s.Rules[0].Definition.Title != prefix+"Retry queue saturated" || s.Rules[0].Definition.Labels["severity"] != sev {
			t.Fatalf("%s: %v %+v", sev, s.Problems, s.Rules)
		}
	}
}

// Every planted failure names the file, the rule id and the bad token.
func TestFlatTierFindings(t *testing.T) {
	cases := []struct {
		name, rel, body string
		rule, token     string
	}{
		{"missing expr", "alerts/q/critical.yaml", strings.Replace(retryQueueFlat, "expr: webhook_retry_queue_depth{\"durian.cloud.service\"=\"zinc\"} > 5000\n", "", 1), "flat-alert-field-missing", `"expr"`},
		{"missing summary", "alerts/q/critical.yaml", strings.Replace(retryQueueFlat, "summary: 'Retry queue at {{ $values.A.Value }}; deliveries dropping'\n", "", 1), "flat-alert-field-missing", `"summary"`},
		{"empty description", "alerts/q/critical.yaml", strings.Replace(retryQueueFlat, "description: 'Check downstream health; the queue drains only if endpoints are up.'", "description: ''", 1), "flat-alert-field-missing", `"description"`},
		{"title too long", "alerts/q/critical.yaml", strings.Replace(retryQueueFlat, "Retry queue saturated", strings.Repeat("x", 49), 1), "flat-alert-title-length", "49 characters"},
		{"datasource", "alerts/q/critical.yaml", strings.Replace(retryQueueFlat, "datasource: metrics", "datasource: loki", 1), "flat-alert-datasource", `"loki"`},
		{"landscape selector", "alerts/q/critical.yaml", strings.Replace(retryQueueFlat, `"durian.cloud.service"="zinc"`, `"durian.cloud.landscape"="raichu"`, 1), "flat-alert-landscape", "landscape"},
		{"landscape plain", "alerts/q/critical.yaml", strings.Replace(retryQueueFlat, `{"durian.cloud.service"="zinc"}`, `{landscape=~"r.*"}`, 1), "flat-alert-landscape", "landscape"},
		{"panel not integer", "alerts/q/critical.yaml", strings.Replace(retryQueueFlat, "panel: 2", "panel: overview", 1), "flat-alert-panel", "overview"},
		{"for not a duration", "alerts/q/critical.yaml", strings.Replace(retryQueueFlat, "for: 10m", "for: soon", 1), "flat-alert-for", `"soon"`},
		{"severity field", "alerts/q/critical.yaml", retryQueueFlat + "severity: critical\n", "flat-alert-field-unknown", "no severity field"},
		{"unknown field", "alerts/q/critical.yaml", retryQueueFlat + "runbook: x\n", "flat-alert-field-unknown", `"runbook"`},
		{"file name is not a severity", "alerts/q/high.yaml", retryQueueFlat, "flat-alert-file-name", "alerts/q/high.yaml"},
		{"too deep", "alerts/q/r/critical.yaml", retryQueueFlat, "flat-alert-file-name", "alerts/q/r/critical.yaml"},
		{"bool modifier", "alerts/q/critical.yaml", strings.Replace(retryQueueFlat, "> 5000", "> bool 5000", 1), "flat-alert-bool", "bool"},
		{"bool modifier in a nested comparison", "alerts/q/critical.yaml", strings.Replace(retryQueueFlat, "expr: webhook_retry_queue_depth{\"durian.cloud.service\"=\"zinc\"} > 5000", "expr: 'sum(rate(x[5m]) > bool 3) > 0'", 1), "flat-alert-bool", "bool"},
		{"emoji in title", "alerts/q/critical.yaml", strings.Replace(retryQueueFlat, "Retry queue saturated", "🚨 Retry queue saturated", 1), "flat-alert-title-emoji", "🚨"},
		{"emoji with variation selector in title", "alerts/q/critical.yaml", strings.Replace(retryQueueFlat, "Retry queue saturated", "Retry queue ⚠️", 1), "flat-alert-title-emoji", "emoji"},
		{"panel null", "alerts/q/critical.yaml", strings.Replace(retryQueueFlat, "panel: 2", "panel: null", 1), "flat-alert-panel", "null"},
		{"panel empty", "alerts/q/critical.yaml", strings.Replace(retryQueueFlat, "panel: 2", "panel:", 1), "flat-alert-panel", "null"},
		{"panel string", "alerts/q/critical.yaml", strings.Replace(retryQueueFlat, "panel: 2", "panel: '2'", 1), "flat-alert-panel", "2"},
		{"panel float", "alerts/q/critical.yaml", strings.Replace(retryQueueFlat, "panel: 2", "panel: 2.5", 1), "flat-alert-panel", "2.5"},
		{"panel bool", "alerts/q/critical.yaml", strings.Replace(retryQueueFlat, "panel: 2", "panel: true", 1), "flat-alert-panel", "true"},
		{"logs landscape backtick field filter", "alerts/q/critical.yaml", logsFlat("`landscape`:raichu"), "flat-alert-landscape", "landscape"},
		{"logs landscape field filter", "alerts/q/critical.yaml", logsFlat("landscape:raichu"), "flat-alert-landscape", "landscape"},
		{"logs landscape dotted field filter", "alerts/q/critical.yaml", logsFlat("\"durian.cloud.landscape\":raichu"), "flat-alert-landscape", "durian.cloud.landscape"},
		{"logs landscape stream selector", "alerts/q/critical.yaml", logsFlat("_stream:{landscape=\"raichu\"}"), "flat-alert-landscape", "landscape"},
		{"logs landscape in a stats conditional", "alerts/q/critical.yaml", logsFlatStats("count() if (\"durian.cloud.landscape\":=raichu) as hits"), "flat-alert-landscape", "landscape"},
		{"logs landscape plain field in a stats conditional", "alerts/q/critical.yaml", logsFlatStats("count() if (landscape:raichu) as hits"), "flat-alert-landscape", "landscape"},
		{"logs landscape in a filter pipe", "alerts/q/critical.yaml", strings.Replace(logsFlat(""), "| stats", "| filter landscape:raichu | stats", 1), "flat-alert-landscape", "landscape"},
		{"logs landscape in a nested source", "alerts/q/critical.yaml", logsFlat("trace_id:in(_time:5m landscape:raichu | fields trace_id)"), "flat-alert-landscape", "landscape"},
		{"unclosed YAML value", "alerts/q/critical.yaml", retryQueueFlat + "summary2: 'unclosed\n", "flat-alert-yaml", "not valid YAML"},
		{"second YAML document", "alerts/q/critical.yaml", retryQueueFlat + "---\ntitle: again\n", "flat-alert-documents", "more than one YAML document"},
		{"logs landscape group key filtered after stats", "alerts/q/critical.yaml", logsRaw("| stats by (\"durian.cloud.landscape\") count() as hits | filter \"durian.cloud.landscape\":=raichu"), "flat-alert-landscape", "group key"},
		{"logs landscape plain group key filtered after stats", "alerts/q/critical.yaml", logsRaw("| stats by (landscape) count() as hits | filter landscape:raichu and hits:>0"), "flat-alert-landscape", "group key"},
		{"flat tier followed by a ConfigMap", "alerts/q/critical.yaml", retryQueueFlat + "---\napiVersion: v1\nkind: ConfigMap\nmetadata: { name: x }\n", "flat-alert-documents", "more than one YAML document"},
		{"flat tier preceded by a marker and followed by a CR", "alerts/q/critical.yaml", "---\n" + retryQueueFlat + "---\nkind: GrafanaAlertRuleGroup\n", "flat-alert-documents", "more than one YAML document"},
		{"uid over 40", "alerts/a-very-long-slug-name-here/critical.yaml", retryQueueFlat, "flat-alert-uid-length", "tracker-zinc-a-very-long-slug-name-here-critical"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := flatDir(t, c.rel, c.body)
			if len(s.Rules) != 0 {
				t.Fatalf("a bad tier file must build no rule: %+v", s.Rules)
			}
			for _, p := range s.Problems {
				if p.File == c.rel && p.Rule == c.rule && strings.Contains(p.Msg, c.token) {
					return
				}
			}
			t.Fatalf("want %s on %s naming %q; got %+v", c.rule, c.rel, c.token, s.Problems)
		})
	}
}

// platform and service of the uid: obs.yaml and the contract; where each came from is in the message.
func TestFlatIdentitySources(t *testing.T) {
	build := func(obs, contract string) *Set {
		dir := t.TempDir()
		if obs != "" {
			write(t, dir, "obs.yaml", obs)
		}
		if contract != "" {
			write(t, dir, "contract.yaml", contract)
		}
		write(t, dir, "alerts/q/critical.yaml", retryQueueFlat)
		s, err := Load(dir)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	if s := build("platform: tracker\nservice: other\n", "service: zinc\n"); len(s.Rules) != 1 || s.Rules[0].Definition.UID != "tracker-other-q-critical" {
		t.Fatalf("obs.yaml service wins over the contract: %+v %v", s.Rules, s.Problems)
	}
	if s := build("platform: tracker\n", "service: zinc\n"); len(s.Rules) != 1 || s.Rules[0].Definition.UID != "tracker-zinc-q-critical" {
		t.Fatalf("service from the contract: %+v %v", s.Rules, s.Problems)
	}
	for name, c := range map[string]struct{ obs, contract, token string }{
		"no platform":          {"", "service: zinc\n", "`platform:` in <observability>/obs.yaml"},
		"no service":           {"platform: tracker\n", "", "the contract's `service:`"},
		"no platform, service": {"", "", "platform and service"},
	} {
		s := build(c.obs, c.contract)
		if len(s.Rules) != 0 || len(s.Problems) != 1 || s.Problems[0].Rule != "flat-alert-identity" || !strings.Contains(s.Problems[0].Msg, c.token) {
			t.Fatalf("%s: %+v %+v", name, s.Rules, s.Problems)
		}
	}
}

// A file that is neither a CR nor a tier file keeps the 0.5 message.
func TestNeitherCRNorTierFileKeepsTheOldMessage(t *testing.T) {
	s := flatDir(t, "alerts/q/critical.yaml", "foo: bar\n")
	if len(s.Problems) != 1 || s.Problems[0].Rule != "" || !strings.Contains(s.Problems[0].Msg, `kind "": want GrafanaAlertRuleGroup`) {
		t.Fatalf("%+v", s.Problems)
	}
}

// logsFlat is a logs tier file whose expr holds extra (a filter) after the event filter.
func logsFlat(extra string) string {
	return "title: Disk corrupted\ndatasource: logs\nexpr: |\n  _stream:{\"durian.cloud.service\"=\"zinc\"} \"durian.cloud/event\":=disk.corrupted " + extra + " | stats count() as hits | filter hits:>0\nfor: 0m\nsummary: s\ndescription: d\n"
}

// Text that only looks like a landscape selector is fine: the check reads parsed label matchers and
// field filters, not the characters of the query.
func TestFlatLandscapeLookAlikesAreNotSelectors(t *testing.T) {
	for name, body := range map[string]string{
		"a quoted message": logsFlat(`"the landscape:raichu is down"`),
		"a label value":    strings.Replace(retryQueueFlat, `"durian.cloud.service"="zinc"`, `"durian.cloud.service"="zinc",job="landscape=raichu"`, 1),
		"a metric name":    strings.Replace(retryQueueFlat, "webhook_retry_queue_depth", "landscape_total", 1),
		"another field":    logsFlat("landscape_name:raichu"),
	} {
		s := flatDir(t, "alerts/q/critical.yaml", body)
		if len(s.Problems) != 0 || len(s.Rules) != 1 {
			t.Errorf("%s: %+v", name, s.Problems)
		}
	}
}

// for is judged by one function everywhere (flat files, obs alerts, the sandbox): 1d is a duration
// Grafana accepts, so it passes here as it does there.
func TestFlatForUsesTheSharedDuration(t *testing.T) {
	for v, ok := range map[string]bool{"0m": true, "10m": true, "1d": true, "1w": true, "2h30m": true, "90s": true, "1d12h": true, "1y": true, "soon": false, "": false, "-5m": false, "1.5d": false, "1.5m": false, "1M": false, "10": false} {
		_, err := ParseFor(v)
		if (err == nil) != ok {
			t.Errorf("ParseFor(%q) = %v, want ok=%v", v, err, ok)
		}
		body := strings.Replace(retryQueueFlat, "for: 10m", "for: '"+v+"'", 1)
		s := flatDir(t, "alerts/q/critical.yaml", body)
		if got := len(s.Rules) == 1; got != ok {
			t.Errorf("flat file with for %q: built=%v, want %v (%+v)", v, got, ok, s.Problems)
		}
	}
	if d, err := ParseFor(""); err == nil {
		t.Errorf("empty for: %v", d)
	}
}

// Only emoji are refused in a title: plain text symbols stay, and a symbol is an emoji when it is a
// code point from U+1F000 up, a BMP Emoji_Presentation one, or is followed by U+FE0F or U+20E3.
func TestFlatTitleEmojiVersusTextSymbols(t *testing.T) {
	ok := []string{"Latency → high", "p99 ↑", "Queue ▲", "Logto™ down", "Replica lag ↑", "Disk ✔ clean", "Rate ≥ 5%", "Temp 90° high", "Cost $5 over"}
	bad := []string{"🚨 Alert", "Disk full 🔥", "⚠️ Warning", "ℹ️ Info", "✅ Done", "⭐ Star", "⌛ Wait", "Queue ❌", "Phone ☎️ down", "1" + string(rune(0x20E3)) + " first"}
	title := func(v string) *Set {
		body := strings.Replace(retryQueueFlat, "title: Retry queue saturated", "title: '"+v+"'", 1)
		return flatDir(t, "alerts/q/critical.yaml", body)
	}
	for _, v := range ok {
		if s := title(v); len(s.Problems) != 0 || len(s.Rules) != 1 {
			t.Errorf("title %q is plain text and must pass: %+v", v, s.Problems)
		}
	}
	for _, v := range bad {
		s := title(v)
		found := false
		for _, p := range s.Problems {
			found = found || p.Rule == "flat-alert-title-emoji"
		}
		if !found || len(s.Rules) != 0 {
			t.Errorf("title %q holds an emoji and must be refused: %+v", v, s.Problems)
		}
	}
}

// logsFlatStats is a logs tier file whose stats function is the given text.
func logsFlatStats(fn string) string {
	return "title: Disk corrupted\ndatasource: logs\nexpr: |\n  _stream:{\"durian.cloud.service\"=\"zinc\"} \"durian.cloud/event\":=disk.corrupted | stats " + fn + " | filter hits:>0\nfor: 0m\nsummary: s\ndescription: d\n"
}

func TestFlatLandscapeLookAlikesInStatsAndSubqueries(t *testing.T) {
	for name, body := range map[string]string{
		"another field in a stats conditional": logsFlatStats("count() if (landscape_name:raichu) as hits"),
		"a quoted message in a conditional":    logsFlatStats(`count() if ("landscape:raichu") as hits`),
		"grouping by landscape is no selector": logsFlatStats("count() as hits by (landscape)"),
		"a subquery on another field":          logsFlat("trace_id:in(_time:5m region:eu | fields trace_id)"),
	} {
		s := flatDir(t, "alerts/q/critical.yaml", body)
		if len(s.Problems) != 0 || len(s.Rules) != 1 {
			t.Errorf("%s: %+v", name, s.Problems)
		}
	}
}

// A file that does not parse but reads as a flat tier file gets a flat rule id; a CR file that does not
// parse keeps 0.5's rule-less message.
func TestUnreadableFlatFileIsClassified(t *testing.T) {
	s := flatDir(t, "alerts/q/critical.yaml", "title: x\nexpr: 'unclosed\n")
	if len(s.Problems) != 1 || s.Problems[0].Rule != "flat-alert-yaml" {
		t.Fatalf("%+v", s.Problems)
	}
	s = flatDir(t, "alerts/q/critical.yaml", "apiVersion: v1\nkind: GrafanaAlertRuleGroup\nspec: 'unclosed\n")
	if len(s.Problems) != 1 || s.Problems[0].Rule != "" {
		t.Fatalf("a CR keeps 0.5's behaviour: %+v", s.Problems)
	}
	s = flatDir(t, "alerts/q/critical.yaml", cr+"---\nkind: Other\n")
	if len(s.Problems) != 1 || s.Problems[0].Rule != "" {
		t.Fatalf("a two-document CR keeps 0.5's behaviour: %+v", s.Problems)
	}
}

// logsRaw is a logs tier file whose expr is the event filter followed by the given pipes.
func logsRaw(pipes string) string {
	return "title: Disk corrupted\ndatasource: logs\nexpr: |\n  _stream:{\"durian.cloud.service\"=\"zinc\"} \"durian.cloud/event\":=disk.corrupted " + pipes + "\nfor: 0m\nsummary: s\ndescription: d\n"
}

// A filter after the stats on a computed field that only shares the name is no landscape selector.
func TestFlatLandscapeNamedComputedFieldIsAllowed(t *testing.T) {
	for name, body := range map[string]string{
		"computed by count":                     logsRaw("| stats count() as landscape | filter landscape:>5"),
		"computed beside a group":               logsRaw("| stats by (service) count() as landscape | filter landscape:>5"),
		"group key, threshold on another field": logsRaw("| stats by (\"durian.cloud.landscape\") count() as hits | filter hits:>5"),
	} {
		s := flatDir(t, "alerts/q/critical.yaml", body)
		if len(s.Problems) != 0 || len(s.Rules) != 1 {
			t.Errorf("%s: %+v", name, s.Problems)
		}
	}
}
