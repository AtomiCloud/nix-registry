package live

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func node(t *testing.T, src string) *yaml.Node {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Content[0]
}

const goodTargets = `
prod:
  metrics: https://vm.prod.example/
  logs: https://vl.prod.example
  traces: https://tempo.prod.example
  grafana: https://grafana.prod.example
  headers:
    Authorization: OBS_PROD_TOKEN
    X-Scope-OrgID: OBS_PROD_ORG
dev:
  metrics: http://vm.dev:8428
`

func TestParseTargetsAndResolveHeadersFromEnvironment(t *testing.T) {
	specs, err := ParseTargets(node(t, goodTargets), "obs.yaml")
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"OBS_PROD_TOKEN": "Bearer abc", "OBS_PROD_ORG": "acme"}
	tg, err := Resolve("prod", specs, nil, func(k string) string { return env[k] }, "obs.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if tg.Metrics != "https://vm.prod.example" {
		t.Fatalf("trailing slash must be trimmed: %q", tg.Metrics)
	}
	if tg.Headers["Authorization"] != "Bearer abc" || tg.Headers["X-Scope-OrgID"] != "acme" {
		t.Fatalf("headers not taken from the environment: %v", tg.Headers)
	}
}

func TestUnsetHeaderVariableIsAnErrorNamingFileLineKeyAndVariable(t *testing.T) {
	specs, _ := ParseTargets(node(t, goodTargets), "obs.yaml")
	_, err := Resolve("prod", specs, nil, func(string) string { return "" }, "obs.yaml")
	if err == nil {
		t.Fatal("an unset variable must be an error")
	}
	for _, want := range []string{"obs.yaml:", "targets.prod.headers.Authorization", "OBS_PROD_TOKEN", "not set"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
}

func TestSecretInTheFileIsRefused(t *testing.T) {
	// A header value that is not a bare environment variable name (a token written into the file) is refused.
	src := "prod:\n  metrics: https://vm\n  headers:\n    Authorization: Bearer abc.def\n"
	_, err := ParseTargets(node(t, src), "obs.yaml")
	if err == nil || !strings.Contains(err.Error(), "targets.prod.headers.Authorization") || !strings.Contains(err.Error(), "environment variable") {
		t.Fatalf("a literal secret must be refused naming the key: %v", err)
	}
}

func TestParseTargetsErrors(t *testing.T) {
	for name, tc := range map[string]struct{ src, want string }{
		"unknown key": {"prod:\n  metricz: https://vm\n", "targets.prod.metricz"},
		"bad url":     {"prod:\n  metrics: vm.example\n", "targets.prod.metrics"},
		"not a map":   {"prod: https://vm\n", "targets.prod"},
		"bad scheme":  {"prod:\n  logs: ftp://vl\n", "targets.prod.logs"},
	} {
		_, err := ParseTargets(node(t, tc.src), "obs.yaml")
		if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "obs.yaml:") {
			t.Errorf("%s: want an error with file:line and %q, got %v", name, tc.want, err)
		}
	}
}

func TestLocalDefaultsWithoutObsYaml(t *testing.T) {
	tg, err := Resolve("local", nil, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if tg.Metrics != "http://127.0.0.1:8428" || tg.Logs != "http://127.0.0.1:9428" || tg.Traces != "http://127.0.0.1:3200" || tg.Grafana != "http://127.0.0.1:3000" {
		t.Fatalf("sandbox addresses: %+v", tg)
	}
}

func TestLocalPrefersRunningSandboxThenFileKeyWins(t *testing.T) {
	sb := map[string]string{"vm": "http://127.0.0.1:18429", "vl": "http://127.0.0.1:18430", "tempo": "http://127.0.0.1:18431", "grafana": "http://127.0.0.1:18428"}
	tg, _ := Resolve("local", nil, sb, nil, "")
	if tg.Metrics != "http://127.0.0.1:18429" || tg.Grafana != "http://127.0.0.1:18428" {
		t.Fatalf("running sandbox ports: %+v", tg)
	}
	specs, _ := ParseTargets(node(t, "local:\n  metrics: http://127.0.0.1:9999\n"), "obs.yaml")
	tg, _ = Resolve("local", specs, sb, nil, "obs.yaml")
	if tg.Metrics != "http://127.0.0.1:9999" || tg.Logs != "http://127.0.0.1:18430" {
		t.Fatalf("a key in the file wins, others fall through: %+v", tg)
	}
}

func TestUnknownTargetListsKnownOnes(t *testing.T) {
	specs, _ := ParseTargets(node(t, goodTargets), "obs.yaml")
	_, err := Resolve("stage", specs, nil, nil, "obs.yaml")
	if err == nil || !strings.Contains(err.Error(), `"stage"`) || !strings.Contains(err.Error(), "dev, prod") {
		t.Fatalf("want the known names listed: %v", err)
	}
	if _, err := Resolve("nope", nil, nil, nil, ""); err == nil || !strings.Contains(err.Error(), "obs.yaml") {
		t.Fatalf("no obs.yaml: say where targets are written: %v", err)
	}
}

func TestParseEstimates(t *testing.T) {
	e, err := ParseEstimates(node(t, "logs: { lines_per_min: 600, bytes_per_min: 120000 }\ntraces: { spans_per_min: 3000 }\n"), "obs.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if e.Logs.LinesPerMin != 600 || e.Logs.BytesPerMin != 120000 || e.Traces.SpansPerMin != 3000 {
		t.Fatalf("%+v", e)
	}
	if _, err := ParseEstimates(node(t, "logs: { lines: 1 }\n"), "obs.yaml"); err == nil || !strings.Contains(err.Error(), "estimates.logs.lines") {
		t.Fatalf("unknown estimate key must be named: %v", err)
	}
	if _, err := ParseEstimates(node(t, "logs: { lines_per_min: -1 }\n"), "obs.yaml"); err == nil {
		t.Fatal("a negative estimate must fail")
	}
}

// tokenShaped values are built at run time, so no literal in the source looks like a credential
// to a secrets scanner.
var tokenShaped = []string{
	"glsa" + "_" + strings.Repeat("Ab1", 12),
	"gh" + "p_" + strings.Repeat("Zy9", 12),
	"gl" + "c_" + strings.Repeat("Qw3", 10),
	"github" + "_pat_" + strings.Repeat("Mn5", 15),
	"AK" + "IA" + strings.Repeat("7", 16),
	"NOTAKNOWNPREFIX" + strings.Repeat("X", 60),
}

func TestTokenShapedHeaderValueIsRefusedAndNeverPrinted(t *testing.T) {
	for _, tok := range tokenShaped {
		src := "prod:\n  metrics: https://vm\n  headers:\n    Authorization: " + tok + "\n"
		_, err := ParseTargets(node(t, src), "obs.yaml")
		if err == nil {
			t.Errorf("%q passes as an environment variable name", tok)
			continue
		}
		if strings.Contains(err.Error(), tok) || !strings.Contains(err.Error(), "targets.prod.headers.Authorization") {
			t.Errorf("the error must name the header and never the value: %v", err)
		}
	}
}

func TestResolveNeverPrintsATokenShapedValue(t *testing.T) {
	// A Spec that did not come through ParseTargets still must not leak: Resolve checks again.
	for _, tok := range tokenShaped {
		specs := map[string]Spec{"prod": {Metrics: "https://vm", Headers: map[string]string{"Authorization": tok}, Line: 3}}
		_, err := Resolve("prod", specs, nil, func(string) string { return "" }, "obs.yaml")
		if err == nil {
			t.Errorf("%q must be refused", tok)
			continue
		}
		if strings.Contains(err.Error(), tok) || !strings.Contains(err.Error(), "Authorization") {
			t.Errorf("the error must name the header and never the value: %v", err)
		}
	}
}

func TestTargetURLsRefuseCredentialsQueryAndFragment(t *testing.T) {
	for name, tc := range map[string]string{
		"credentials":          "https://admin:hunter2secret@vm.example",
		"user only":            "https://hunter2secret@vm.example",
		"query":                "https://vm.example/?token=hunter2secret",
		"empty query":          "https://vm.example/?",
		"fragment":             "https://vm.example/#hunter2secret",
		"credentials and path": "https://admin:hunter2secret@vm.example/prefix",
	} {
		src := "prod:\n  metrics: " + tc + "\n"
		_, err := ParseTargets(node(t, src), "obs.yaml")
		if err == nil {
			t.Errorf("%s: %q must be refused", name, tc)
			continue
		}
		if strings.Contains(err.Error(), "hunter2secret") || !strings.Contains(err.Error(), "targets.prod.metrics") {
			t.Errorf("%s: the error must name the key and never echo the URL: %v", name, err)
		}
	}
}
