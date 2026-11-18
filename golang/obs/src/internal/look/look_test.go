package look

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AtomiCloud/obs/internal/sandbox"
)

var update = flag.Bool("update", false, "rewrite the golden prompt")

const checklistMD = `# Standards

## Visual checklist ("looks good" defined)

- No "No data" or error panels with synthetic data loaded
- Units render as units (` + "`1.2k req/s`" + `, ` + "`250 ms`" + `), not raw numbers
- Legends readable and bounded; panel not swallowed by
  the legend
- Every template variable populates and visibly changes the view

---

## Other section

- not a checklist item
`

const seedYAML = `length: 30m
random_seed: 7
resource:
  durian.cloud.landscape: fixture-dev
  durian.cloud.platform: payments
  durian.cloud.service: checkout
  durian.cloud.module: api
traffic: { requests: 2/s, shape: flat }
metrics:
  checkout.charges:
    type: counter
    series:
      - { where: { outcome: success }, share: 0.9 }
      - { where: { outcome: error }, share: 0.1 }
logs:
  - { event: checkout.charge.failed, level: error, line: charge failed, from: { metric: checkout.charges, where: { outcome: error } } }
`

const dashJSONText = `{"uid":"payments-overview","title":"Payments overview","time":{"from":"now-3h","to":"now"},
"templating":{"list":[
 {"type":"datasource","name":"ds","query":"prometheus"},
 {"type":"query","name":"outcome","query":{"query":"label_values(checkout_charges_total, outcome)"}},
 {"type":"query","name":"provider","query":"label_values(checkout_charges_total, payment.provider)","includeAll":true,"multi":true},
 {"type":"custom","name":"route","query":"/charge,/refund","current":{"value":"/refund"}},
 {"type":"custom","name":"first","query":"a,b"},
 {"type":"interval","name":"win"}
]},
"panels":[
 {"id":1,"title":"Charges","type":"timeseries"},
 {"id":10,"title":"Details","type":"row","panels":[{"id":3,"title":"Errors","type":"logs"}]},
 {"id":2,"title":"Latency","type":"timeseries"}
]}`

func project(t *testing.T) (dir, checklist string) {
	t.Helper()
	root := t.TempDir()
	dir = filepath.Join(root, "observability")
	if err := os.MkdirAll(filepath.Join(dir, "dashboards"), 0o755); err != nil {
		t.Fatal(err)
	}
	for f, c := range map[string]string{
		"seed.yaml": seedYAML, "dashboards/overview.json": dashJSONText, "dashboards/nouid.json": `{"title":"No uid","panels":[]}`,
	} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	checklist = filepath.Join(root, "index.md")
	if err := os.WriteFile(checklist, []byte(checklistMD), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, checklist
}

func fakeUp(info *sandbox.Info, err error) func(context.Context) (*sandbox.Info, error) {
	return func(context.Context) (*sandbox.Info, error) { return info, err }
}

var testInfo = &sandbox.Info{
	Project: "demo", Grafana: "http://127.0.0.1:3000",
	URLs:        map[string]string{"grafana": "http://127.0.0.1:3000"},
	From:        time.Date(2026, 11, 17, 9, 30, 0, 0, time.UTC).UnixMilli(),
	To:          time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC).UnixMilli(),
	Datasources: map[string][]string{"metrics": {"payments-metrics"}, "logs": {"payments-logs"}},
}

func TestPromptGolden(t *testing.T) {
	dir, cl := project(t)
	p := Setup(context.Background(), Options{Dir: dir, Base: "origin/main", Checklist: cl, Out: "obs-look", Project: "demo", Runtime: "docker",
		Files: []string{"dashboards/overview.json", "dashboards/nouid.json"}}, fakeUp(testInfo, nil))
	if !p.OK || len(p.Dashboards) != 2 {
		t.Fatalf("plan: ok=%v err=%q dashboards=%d warnings=%v", p.OK, p.Error, len(p.Dashboards), p.Warnings)
	}
	// The temp path of the checklist is not stable: the golden shows a fixed name.
	got := strings.ReplaceAll(p.Prompt, cl, "docs/standards/grafana-dashboards/index.md")
	golden := filepath.Join("testdata", "prompt.golden")
	if *update {
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("prompt differs from %s (run go test ./internal/look -update to rewrite it)\n--- got ---\n%s", golden, got)
	}
}

func TestPlanFacts(t *testing.T) {
	dir, cl := project(t)
	p := Setup(context.Background(), Options{Dir: dir, Checklist: cl, Files: []string{"dashboards/overview.json", "dashboards/nouid.json"}}, fakeUp(testInfo, nil))
	d := p.Dashboards[0]
	if d.UID != "payments-overview" || p.Dashboards[1].UID != "obs-dashboards-nouid" {
		t.Errorf("uids %q %q", d.UID, p.Dashboards[1].UID)
	}
	for _, want := range []string{
		"http://127.0.0.1:3000/d/payments-overview/payments-overview?orgId=1&from=", "&to=", "&var-ds=payments-metrics",
		"&var-outcome=error", "&var-provider=%24__all", "&var-route=%2Frefund", "&var-first=a", "&var-win=5m", "&kiosk",
	} {
		if !strings.Contains(d.URL, want) {
			t.Errorf("url lacks %q: %s", want, d.URL)
		}
	}
	if d.Shot != "obs-look/dashboards-overview.png" {
		t.Errorf("shot %s", d.Shot)
	}
	// Panels: rows are not panels, nested ones are, sorted by id.
	var ids []int64
	for _, pn := range d.Panels {
		ids = append(ids, pn.ID)
	}
	if len(ids) != 3 || ids[0] != 1 || ids[1] != 2 || ids[2] != 3 {
		t.Errorf("panel ids %v", ids)
	}
	if !strings.Contains(d.Panels[0].URL, "viewPanel=panel-1") {
		t.Errorf("panel url %s", d.Panels[0].URL)
	}
	// The checklist: four items, the wrapped one joined, the next section not read.
	if len(p.Checklist) != 4 || p.Checklist[2] != "Legends readable and bounded; panel not swallowed by the legend" {
		t.Errorf("checklist %q", p.Checklist)
	}
	// --json carries the same content and the prompt.
	b, err := json.Marshal(p)
	if err != nil || !strings.Contains(string(b), `"prompt":"obs look: review 2 dashboard(s)`) || !strings.Contains(string(b), `"dashboards"`) {
		t.Errorf("json: %v %.200s", err, b)
	}
}

func TestSetupFailureIsAPlanNotAnError(t *testing.T) {
	dir, cl := project(t)
	p := Setup(context.Background(), Options{Dir: dir, Checklist: cl, Files: []string{"dashboards/overview.json"}}, fakeUp(nil, errors.New("docker is not running")))
	if p.OK || !strings.Contains(p.Error, "sandbox setup failed: docker is not running") {
		t.Fatalf("plan %+v", p)
	}
	for _, want := range []string{"setup failed, nothing to review", "exits 0 on purpose", "docker is not running", "obs sandbox down"} {
		if !strings.Contains(p.Prompt, want) {
			t.Errorf("prompt lacks %q:\n%s", want, p.Prompt)
		}
	}
}

func TestNoChangedDashboardsStartsNothing(t *testing.T) {
	dir, cl := project(t)
	called := false
	p := Setup(context.Background(), Options{Dir: dir, Base: "origin/main", Checklist: cl,
		Git: func(string, ...string) (string, error) { return "", nil }},
		func(context.Context) (*sandbox.Info, error) { called = true; return testInfo, nil })
	if !p.OK || called || !strings.Contains(p.Prompt, "no changed dashboards (against origin/main)") {
		t.Errorf("ok=%v called=%v prompt=%q", p.OK, called, p.Prompt)
	}
}

func TestChangedDashboardsFromGit(t *testing.T) {
	dir, cl := project(t)
	var calls []string
	git := func(_ string, args ...string) (string, error) {
		calls = append(calls, strings.Join(args, " "))
		switch args[0] {
		case "merge-base":
			return "abc123\n", nil
		case "diff":
			return "dashboards/overview.json\ndashboards/readme.md\n", nil
		case "ls-files":
			return "dashboards/nouid.json\ndashboards/overview.json\n", nil
		}
		return "", nil
	}
	p := Setup(context.Background(), Options{Dir: dir, Base: "origin/main", Checklist: cl, Git: git}, fakeUp(testInfo, nil))
	if len(p.Dashboards) != 2 || p.Dashboards[0].File != "dashboards/nouid.json" || p.Dashboards[1].File != "dashboards/overview.json" {
		t.Fatalf("dashboards %+v warnings %v", p.Dashboards, p.Warnings)
	}
	want := []string{"merge-base origin/main HEAD", "diff --name-only --relative --diff-filter=ACMR abc123 -- dashboards", "ls-files --others --exclude-standard -- dashboards"}
	if strings.Join(calls, "|") != strings.Join(want, "|") {
		t.Errorf("git calls %q", calls)
	}
}

func TestGitFailureIsAPlan(t *testing.T) {
	dir, cl := project(t)
	p := Setup(context.Background(), Options{Dir: dir, Base: "origin/nope", Checklist: cl,
		Git: func(string, ...string) (string, error) { return "", errors.New("git diff: bad revision") }}, fakeUp(testInfo, nil))
	if p.OK || !strings.Contains(p.Error, "cannot tell which dashboards changed") || !strings.Contains(p.Error, "bad revision") {
		t.Errorf("%+v", p)
	}
}

// The fixture file names "checklist" in its title and in the real heading: the section with items wins.
func TestChecklistTitleMentioningChecklistIsNotTheSection(t *testing.T) {
	p := filepath.Join(t.TempDir(), "i.md")
	_ = os.WriteFile(p, []byte("# Standards (the visual checklist is below)\n\nintro\n\n### Visual checklist\n\n- one\n- two\n\n## Next\n\n- x\n"), 0o644)
	items, err := ReadChecklist(p)
	if err != nil || len(items) != 2 || items[0] != "one" {
		t.Errorf("%q %v", items, err)
	}
}

func TestChecklistProblemsAreWarnings(t *testing.T) {
	dir, _ := project(t)
	p := Setup(context.Background(), Options{Dir: dir, Checklist: filepath.Join(dir, "missing.md"), Files: []string{"dashboards/overview.json"}}, fakeUp(testInfo, nil))
	if !p.OK || len(p.Warnings) != 1 || !strings.Contains(p.Warnings[0], "pass --checklist") || !strings.Contains(p.Prompt, "do not invent items") {
		t.Errorf("%+v", p)
	}
	empty := filepath.Join(dir, "empty.md")
	_ = os.WriteFile(empty, []byte("# Visual checklist\n\nnothing\n"), 0o644)
	if _, err := ReadChecklist(empty); err == nil || !strings.Contains(err.Error(), "no bullet items") {
		t.Errorf("empty checklist: %v", err)
	}
}

func TestBadDashboardFileIsSkippedWithAWarning(t *testing.T) {
	dir, cl := project(t)
	_ = os.WriteFile(filepath.Join(dir, "dashboards", "broken.json"), []byte("{"), 0o644)
	p := Setup(context.Background(), Options{Dir: dir, Checklist: cl, Files: []string{"dashboards/broken.json", "dashboards/overview.json"}}, fakeUp(testInfo, nil))
	if !p.OK || len(p.Dashboards) != 1 || len(p.Warnings) != 1 || !strings.Contains(p.Warnings[0], "dashboards/broken.json: skipped") {
		t.Errorf("%+v", p)
	}
}

func TestEmptySandboxWindowOmitsTimeRange(t *testing.T) {
	dir, cl := project(t)
	info := *testInfo
	info.From, info.To = 0, 0
	p := Setup(context.Background(), Options{Dir: dir, Checklist: cl, Files: []string{"dashboards/overview.json"}}, fakeUp(&info, nil))
	if strings.Contains(p.Dashboards[0].URL, "from=") || !strings.Contains(p.Prompt, "no seed was loaded") {
		t.Errorf("%s\n%s", p.Dashboards[0].URL, p.Prompt)
	}
}

// A saved "current" datasource uid that the sandbox does not provision must not reach the URL:
// the sandbox's own datasource is used. A current uid that IS provisioned stays.
func TestDatasourceVariableResolvesAgainstTheSandboxFirst(t *testing.T) {
	dir, cl := project(t)
	for name, tc := range map[string]struct{ current, want, source string }{
		"stale":       {"grafana-prod-metrics", "payments-metrics", "sandbox datasource"},
		"provisioned": {"payments-metrics", "payments-metrics", "current"},
		"empty":       {"", "payments-metrics", "sandbox datasource"},
	} {
		dash := fmt.Sprintf(`{"uid":"ds-%s","title":"Ds","templating":{"list":[{"type":"datasource","name":"ds","query":"prometheus","current":{"value":%q}}]},"panels":[]}`, name, tc.current)
		if err := os.WriteFile(filepath.Join(dir, "dashboards", name+".json"), []byte(dash), 0o644); err != nil {
			t.Fatal(err)
		}
		p := Setup(context.Background(), Options{Dir: dir, Checklist: cl, Files: []string{"dashboards/" + name + ".json"}}, fakeUp(testInfo, nil))
		if len(p.Dashboards) != 1 || len(p.Dashboards[0].Vars) != 1 {
			t.Fatalf("%s: plan %+v warnings %v", name, p.Dashboards, p.Warnings)
		}
		v := p.Dashboards[0].Vars[0]
		if len(v.Values) != 1 || v.Values[0] != tc.want || v.Source != tc.source {
			t.Errorf("%s: $ds = %v (%s), want %s (%s)", name, v.Values, v.Source, tc.want, tc.source)
		}
		if strings.Contains(p.Dashboards[0].URL, tc.current) && tc.current != tc.want && tc.current != "" {
			t.Errorf("%s: the stale uid %s is in the URL: %s", name, tc.current, p.Dashboards[0].URL)
		}
	}
}

// The cleanup instruction names the exact project and runtime of this run.
func TestCleanupCommandNamesProjectAndRuntime(t *testing.T) {
	dir, cl := project(t)
	info := *testInfo
	info.Project = "my-repo"
	p := Setup(context.Background(), Options{Dir: dir, Checklist: cl, Project: "my-repo", Runtime: "podman", Files: []string{"dashboards/overview.json"}}, fakeUp(&info, nil))
	if want := "`obs sandbox down --project my-repo --runtime podman`"; !strings.Contains(p.Prompt, want) {
		t.Errorf("prompt lacks %s:\n%s", want, p.Prompt)
	}
	if strings.Contains(p.Prompt, "`obs sandbox down` ") {
		t.Errorf("a bare `obs sandbox down` is left in the prompt")
	}
	// The failure text names them too (the sandbox may be half up).
	f := Setup(context.Background(), Options{Dir: dir, Checklist: cl, Project: "my-repo", Runtime: "podman", Files: []string{"dashboards/overview.json"}}, fakeUp(nil, fmt.Errorf("boom")))
	if want := "obs sandbox down --project my-repo --runtime podman"; !strings.Contains(f.Prompt, want) {
		t.Errorf("failure prompt lacks %s:\n%s", want, f.Prompt)
	}
}

const tracesDash = `{"uid":"svc-traces","title":"Service traces","time":{"from":"now-3h","to":"now"},
"templating":{"list":[{"type":"datasource","name":"tdb","query":"tempo"},{"type":"datasource","name":"pdb","query":"grafana-pyroscope-datasource"}]},
"panels":[
 {"id":1,"title":"Request rate","type":"timeseries","datasource":{"type":"prometheus","uid":"payments-metrics"},"targets":[{"refId":"A","expr":"up"}]},
 {"id":2,"title":"Slow traces","type":"traces","datasource":{"type":"tempo","uid":"obs-traces"},"targets":[{"refId":"A","queryType":"traceql","query":"{}"}]},
 {"id":3,"title":"Trace table","type":"table","datasource":{"type":"datasource","uid":"${tdb}"},"targets":[{"refId":"A","datasource":{"type":"tempo","uid":"${tdb}"}}]},
 {"id":4,"title":"CPU flame graph","type":"flamegraph","datasource":{"type":"grafana-pyroscope-datasource","uid":"${pdb}"},"targets":[{"refId":"A"}]},
 {"id":5,"title":"Mixed","type":"timeseries","datasource":{"type":"datasource","uid":"-- Mixed --"},"targets":[{"refId":"A","datasource":{"type":"tempo","uid":"obs-traces"}},{"refId":"B","datasource":{"type":"prometheus","uid":"payments-metrics"}}]},
 {"id":6,"title":"Uid only","type":"table","datasource":{"uid":"primordial-tempo"},"targets":[{"refId":"A"}]}
]}`

func TestTraceAndProfilePanelsAreExpectedEmpty(t *testing.T) {
	dir, cl := project(t)
	if err := os.WriteFile(filepath.Join(dir, "dashboards", "traces.json"), []byte(tracesDash), 0o644); err != nil {
		t.Fatal(err)
	}
	p := Setup(context.Background(), Options{Dir: dir, Checklist: cl, Project: "demo", Runtime: "docker", Files: []string{"dashboards/traces.json"}}, fakeUp(testInfo, nil))
	if len(p.Dashboards) != 1 {
		t.Fatalf("plan: %v %v", p.Error, p.Warnings)
	}
	got := map[int64]string{}
	for _, pn := range p.Dashboards[0].Panels {
		got[pn.ID] = pn.ExpectedEmpty
	}
	want := map[int64]string{1: "", 2: "traces", 3: "traces", 4: "profiles", 5: "", 6: "traces"}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("panel %d expected-empty %q, want %q", id, got[id], w)
		}
	}
	for _, w := range []string{
		`expected empty in the sandbox; mark C1 n/a`, `panel 2 "Slow traces"`, `panel 4 "CPU flame graph"`,
	} {
		if !strings.Contains(p.Prompt, w) {
			t.Errorf("prompt lacks %q:\n%s", w, p.Prompt)
		}
	}
	got2 := strings.ReplaceAll(p.Prompt, cl, "docs/standards/grafana-dashboards/index.md")
	golden := filepath.Join("testdata", "prompt-traces.golden")
	if *update {
		if err := os.WriteFile(golden, []byte(got2), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	wantB, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if got2 != string(wantB) {
		t.Errorf("prompt differs from %s (run go test ./internal/look -update to rewrite it)\n--- got ---\n%s", golden, got2)
	}
}
