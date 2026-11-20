package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AtomiCloud/obs/internal/files"
)

// Fix round 128 (review of the obs check / obs test PR): one test per finding, each red before the fix.

const fixBase = "1700000000" // 14 Nov 2023: the seed's time line ends in the past on every machine

// fixFolder copies tests/fixtures/test/good/observability to a temp folder and returns it.
func fixFolder(t *testing.T) string {
	t.Helper()
	src := filepath.Join("..", "tests", "fixtures", "test", "good", "observability")
	dst := filepath.Join(t.TempDir(), "observability")
	err := filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		to := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(to, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(to, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

func fixWrite(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fixEdit(t *testing.T, dir, rel, old, now string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), old) {
		t.Fatalf("%s: %q not found", rel, old)
	}
	fixWrite(t, dir, rel, strings.Replace(string(b), old, now, 1))
}

func fixRun(f func([]string, io.Writer, io.Writer) int, args ...string) (int, string) {
	var out, errb bytes.Buffer
	rc := f(args, &out, &errb)
	return rc, out.String() + errb.String()
}

func mustHave(t *testing.T, out string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("output lacks %q:\n%s", w, out)
		}
	}
}

// ---- 1: a contract.yaml that cannot be read is exit 2, as in 0.4 ------------------------------------

func TestCheckContractSymlinkLoopIsExit2(t *testing.T) {
	dir := fixFolder(t)
	if err := os.Remove(filepath.Join(dir, "contract.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("contract.yaml", filepath.Join(dir, "contract.yaml")); err != nil {
		t.Fatal(err)
	}
	rc, out := fixRun(runCheck, "--dir", dir)
	if rc != 2 {
		t.Fatalf("exit %d, want 2 (0.4 stopped on an unreadable contract):\n%s", rc, out)
	}
	mustHave(t, out, "obs check:", "too many levels of symbolic links")
	if strings.Contains(out, "limits not checked") {
		t.Errorf("an unreadable contract must not be reported as an absent one:\n%s", out)
	}
}

func TestCheckAbsentContractStillSkipsLimits(t *testing.T) {
	dir := fixFolder(t)
	if err := os.Remove(filepath.Join(dir, "contract.yaml")); err != nil {
		t.Fatal(err)
	}
	rc, out := fixRun(runCheck, "--dir", dir)
	if rc != 0 {
		t.Fatalf("exit %d, want 0:\n%s", rc, out)
	}
	mustHave(t, out, "contract.yaml not found: limits")
}

// ---- 2: obs test accepts an empty expectation; obs alerts still refuses it --------------------------

func addEmptyIncident(t *testing.T, dir string) {
	t.Helper()
	fixWrite(t, dir, "incidents/empty.yaml", "{ story: A disturbance nobody asserts on, window: { start: 25m, duration: 5m }, change: [{ metric: fixture.requests, where: { outcome: error }, rate: 0.1/s }], expect: {} }\n")
	fixEdit(t, dir, "seed.yaml", "incidents: [spike]", "incidents: [spike, empty]")
}

func TestObsTestEmptyExpectIsUncaughtNotInputError(t *testing.T) {
	dir := fixFolder(t)
	addEmptyIncident(t, dir)
	rc, out := fixRun(runTest, "--dir", dir, "--base", fixBase)
	if rc != 1 {
		t.Fatalf("exit %d, want 1 (a missed incident is a red verdict, not an input error):\n%s", rc, out)
	}
	mustHave(t, out, "FAIL incidents/empty.yaml incident empty window [25m0s,30m0s): [incident-uncaught]", "obs test: incidents: FAILED: 2 incidents, 1 caught, 1 uncaught", "obs test: alerts: ok:")
}

func TestObsTestEmptyExpectWithNoRulesIsUncaught(t *testing.T) {
	dir := fixFolder(t)
	addEmptyIncident(t, dir)
	if err := os.RemoveAll(filepath.Join(dir, "alerts")); err != nil {
		t.Fatal(err)
	}
	rc, out := fixRun(runTest, "--dir", dir, "--base", fixBase)
	if rc != 1 {
		t.Fatalf("exit %d, want 1:\n%s", rc, out)
	}
	mustHave(t, out, "incident empty window [25m0s,30m0s): [incident-uncaught]")
}

func TestObsAlertsStillRefusesEmptyExpect(t *testing.T) {
	dir := fixFolder(t)
	addEmptyIncident(t, dir)
	rc, out := fixRun(runAlerts, "--dir", dir, "--base", fixBase)
	if rc != 2 {
		t.Fatalf("obs alerts exit %d, want 2 (its validation is unchanged):\n%s", rc, out)
	}
	mustHave(t, out, "expect needs fire or quiet alert IDs")
}

// ---- 3: a late resolve keeps its reason in the uncaught line ----------------------------------------

func TestObsTestLateResolveKeepsTheFailedCondition(t *testing.T) {
	dir := fixFolder(t)
	// keepFiringFor 20m: the alert fires inside the incident [10m,20m) and keeps firing far past resolve_within.
	fixEdit(t, dir, "alerts/metric/critical.yaml", "for: 1m,", "for: 1m,\n            keepFiringFor: 20m,")
	rc, out := fixRun(runTest, "--dir", dir, "--base", fixBase)
	if rc != 1 {
		t.Fatalf("exit %d, want 1:\n%s", rc, out)
	}
	mustHave(t, out, "[incident-uncaught] no alert passed its fire expectation: metric (fired at +", "firing outside window")
	if strings.Contains(out, "fired inside the window") {
		t.Errorf("the alert did fire inside the window; the line must not say it did not:\n%s", out)
	}
}

// ---- 4: --event-rule off reaches the alerts part of obs test ----------------------------------------

const logsNoEventAlert = `{
  apiVersion: grafana.integreatly.org/v1beta1,
  kind: GrafanaAlertRuleGroup,
  metadata: { name: fixture-logs },
  spec:
    {
      interval: 1m,
      rules:
        [
          {
            uid: logs,
            title: logs,
            condition: C,
            for: 1m,
            data:
              [
                {
                  refId: A,
                  datasourceUid: fixture-logs,
                  queryType: stats,
                  relativeTimeRange: { from: 60, to: 0 },
                  model:
                    {
                      datasource: { type: victoriametrics-logs-datasource, uid: fixture-logs },
                      queryType: stats,
                      expr: '_stream:{"service.name"="checkout"} | stats count() as hits',
                    },
                },
                { refId: C, datasourceUid: __expr__, model: { type: math, expression: $A > 2000 } },
              ],
          },
        ],
    },
}
`

func logsNoEventFolder(t *testing.T) string {
	dir := fixFolder(t)
	fixWrite(t, dir, "contract.yaml", `{
  service: fixture,
  types: { outcome: { kind: enum, values: [ok, error] } },
  metrics: { fixture.requests: { kind: counter, budget: 2, labels: { outcome: outcome } } },
  logs: { Failed: { event: fixture.failed, level: error } },
}
`)
	fixEdit(t, dir, "seed.yaml", "share: 1 }, { where: { outcome: error }, share: 0 }", "share: 0.9 }, { where: { outcome: error }, share: 0.1 }")
	fixEdit(t, dir, "seed.yaml", "  incidents: [spike],", "  logs: [{ event: fixture.failed, level: error, from: { metric: fixture.requests, where: { outcome: error } } }],\n  incidents: [spike],")
	fixWrite(t, dir, "alerts/logs/critical.yaml", logsNoEventAlert)
	return dir
}

func TestObsTestEventRuleOffReachesAlerts(t *testing.T) {
	dir := logsNoEventFolder(t)
	rc, out := fixRun(runTest, "--dir", dir, "--base", fixBase)
	if rc != 1 || !strings.Contains(out, "[logsql-alert-needs-event]") {
		t.Fatalf("baseline (event rule auto): exit %d, want 1 with [logsql-alert-needs-event]:\n%s", rc, out)
	}
	rc, out = fixRun(runTest, "--dir", dir, "--base", fixBase, "--event-rule", "off")
	if rc != 0 {
		t.Fatalf("--event-rule off: exit %d, want 0 (the override reaches the alerts part):\n%s", rc, out)
	}
	mustHave(t, out, "obs test: alerts: ok:", "obs test: ok")
	if strings.Contains(out, "[logsql-alert-needs-event]") {
		t.Errorf("--event-rule off still prints the rule:\n%s", out)
	}
}

func TestObsAlertsHasNoEventRuleFlagAndKeepsItsDefault(t *testing.T) {
	// obs alerts is unchanged: no flag, the rule stays on when a contract exists.
	dir := logsNoEventFolder(t)
	rc, out := fixRun(runAlerts, "--dir", dir, "--base", fixBase)
	if rc != 1 || !strings.Contains(out, "[logsql-alert-needs-event]") {
		t.Fatalf("obs alerts: exit %d, want 1 with [logsql-alert-needs-event]:\n%s", rc, out)
	}
}

// ---- 7: links run whenever the contract declares spans or a query is TraceQL ------------------------

func TestObsTestRunsLinksWhenContractDeclaresSpansButSeedHasNoTraces(t *testing.T) {
	dir := fixFolder(t)
	fixWrite(t, dir, "contract.yaml", `{
  service: fixture,
  types: { outcome: { kind: enum, values: [ok, error] } },
  metrics: { fixture.requests: { kind: counter, budget: 2, labels: { outcome: outcome } } },
  spans: { fixture.charge: { attributes: { outcome: outcome } } },
}
`)
	if rc, out := fixRun(runLinks, "--dir", dir, "--base", fixBase); rc != 1 {
		t.Fatalf("premise: obs links exit %d, want 1:\n%s", rc, out)
	}
	rc, out := fixRun(runTest, "--dir", dir, "--base", fixBase)
	if rc != 1 {
		t.Fatalf("obs test exit %d, want 1 (obs links is red on this folder):\n%s", rc, out)
	}
	mustHave(t, out, "[seed] no traces in seed.traces", "obs test: links: FAILED:")
	if strings.Contains(out, "links: skipped") {
		t.Errorf("links must not be skipped when the contract declares spans:\n%s", out)
	}
}

func TestObsTestSkipsLinksWhenNoSpansAndNoTraceQL(t *testing.T) {
	dir := fixFolder(t)
	rc, out := fixRun(runTest, "--dir", dir, "--base", fixBase)
	if rc != 0 {
		t.Fatalf("exit %d, want 0:\n%s", rc, out)
	}
	mustHave(t, out, "obs test: links: skipped:")
}

// ---- 9: a deliberate non-incident disturbance is a note, not a red ----------------------------------

const quietOnlyIncident = `{
  story: A burst the alerts must ignore,
  window: { start: 25m, duration: 5m },
  change: [{ metric: fixture.requests, where: { outcome: error }, rate: 0.01/s }],
  expect: { quiet: [metric], fire_within: 5m, resolve_within: 5m },
`

func addQuietIncident(t *testing.T, dir, marker string) {
	t.Helper()
	fixWrite(t, dir, "incidents/blip.yaml", quietOnlyIncident+marker+"}\n")
	fixEdit(t, dir, "seed.yaml", "incidents: [spike]", "incidents: [spike, blip]")
}

func TestQuietOnlyIncidentWithoutMarkerStaysRed(t *testing.T) {
	dir := fixFolder(t)
	addQuietIncident(t, dir, "")
	rc, out := fixRun(runTest, "--dir", dir, "--base", fixBase)
	if rc != 1 {
		t.Fatalf("exit %d, want 1:\n%s", rc, out)
	}
	mustHave(t, out, "incidents/blip.yaml incident blip window [25m0s,30m0s): [incident-uncaught] expect.fire is empty")
}

func TestDisturbanceMarkerExcludesAndPrintsANote(t *testing.T) {
	dir := fixFolder(t)
	addQuietIncident(t, dir, `  disturbance: "proves the error alert ignores a trickle",`+"\n")
	rc, out := fixRun(runTest, "--dir", dir, "--base", fixBase)
	if rc != 0 {
		t.Fatalf("exit %d, want 0:\n%s", rc, out)
	}
	mustHave(t, out, "[disturbance] incidents/blip.yaml blip: proves the error alert ignores a trickle", "obs test: incidents: ok: 2 incidents, 1 caught, 0 uncaught, 1 disturbance(s)", "obs test: ok")
	if strings.Contains(out, "[incident-uncaught]") {
		t.Errorf("a disturbance is excluded from [incident-uncaught]:\n%s", out)
	}
}

func TestDisturbanceWithEmptyReasonNamesFileAndLine(t *testing.T) {
	for name, marker := range map[string]string{"empty string": "  disturbance: \"\",\n", "null": "  disturbance: ~,\n"} {
		t.Run(name, func(t *testing.T) {
			dir := fixFolder(t)
			addQuietIncident(t, dir, marker)
			rc, out := fixRun(runTest, "--dir", dir, "--base", fixBase)
			if rc != 2 {
				t.Fatalf("exit %d, want 2:\n%s", rc, out)
			}
			mustHave(t, out, filepath.Join(dir, "incidents", "blip.yaml")+":6", "disturbance")
		})
	}
}

func TestObsAlertsIgnoresTheDisturbanceMarker(t *testing.T) {
	plain, marked := fixFolder(t), fixFolder(t)
	addQuietIncident(t, plain, "")
	addQuietIncident(t, marked, `  disturbance: "proves the error alert ignores a trickle",`+"\n")
	rc1, out1 := fixRun(runAlerts, "--dir", plain, "--base", fixBase)
	rc2, out2 := fixRun(runAlerts, "--dir", marked, "--base", fixBase)
	if rc1 != rc2 || out1 != out2 {
		t.Fatalf("obs alerts output differs with the marker (rc %d vs %d):\n--- plain\n%s\n--- marked\n%s", rc1, rc2, out1, out2)
	}
}

func TestTracesExpectedForATraceQLQuery(t *testing.T) {
	dir := fixFolder(t) // a contract with no spans
	if tracesExpected(&files.Set{}, dir) {
		t.Error("no span and no TraceQL query: nothing is expected")
	}
	if !tracesExpected(&files.Set{Queries: []files.Query{{Kind: files.Traces}}}, dir) {
		t.Error("a TraceQL query expects traces")
	}
}

// ---- fix round 2: a dangling contract symlink, disturbance by merge, disturbance with fire ---------

func TestCheckContractDanglingSymlinkIsExit2(t *testing.T) {
	dir := fixFolder(t)
	if err := os.Remove(filepath.Join(dir, "contract.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("missing-target.yaml", filepath.Join(dir, "contract.yaml")); err != nil {
		t.Fatal(err)
	}
	rc, out := fixRun(runCheck, "--dir", dir)
	if rc != 2 {
		t.Fatalf("exit %d, want 2 (a dangling contract.yaml symlink is an unreadable contract, not an absent one):\n%s", rc, out)
	}
	mustHave(t, out, "obs check:", "contract.yaml: no such file or directory")
	if strings.Contains(out, "limits not checked") {
		t.Errorf("a dangling symlink must not be reported as an absent contract:\n%s", out)
	}
}

func TestDisturbanceEmptyReasonViaMergeNamesFileAndLine(t *testing.T) {
	for name, marker := range map[string]string{
		"inline merge": "  <<: { disturbance: \"\" },\n",
		"null merge":   "  <<: { disturbance: ~ },\n",
	} {
		t.Run(name, func(t *testing.T) {
			dir := fixFolder(t)
			addQuietIncident(t, dir, marker)
			rc, out := fixRun(runTest, "--dir", dir, "--base", fixBase)
			if rc != 2 {
				t.Fatalf("exit %d, want 2 (a merged empty reason is still an empty reason):\n%s", rc, out)
			}
			mustHave(t, out, filepath.Join(dir, "incidents", "blip.yaml")+":6", "disturbance needs a non-empty reason")
		})
	}
}

func TestDisturbanceViaMergeWithReasonIsStillANote(t *testing.T) {
	dir := fixFolder(t)
	addQuietIncident(t, dir, "  <<: { disturbance: \"proves the error alert ignores a trickle\" },\n")
	rc, out := fixRun(runTest, "--dir", dir, "--base", fixBase)
	if rc != 0 {
		t.Fatalf("exit %d, want 0:\n%s", rc, out)
	}
	mustHave(t, out, "[disturbance] incidents/blip.yaml blip: proves the error alert ignores a trickle")
}

func TestDisturbanceOnAnIncidentWithFireIsAnError(t *testing.T) {
	dir := fixFolder(t)
	fixWrite(t, dir, "incidents/blip.yaml", `{
  story: A burst with a fire expectation,
  window: { start: 25m, duration: 5m },
  change: [{ metric: fixture.requests, where: { outcome: error }, rate: 0.01/s }],
  expect: { fire: [metric], fire_within: 5m, resolve_within: 5m },
  disturbance: "this one is not quiet-only",
}
`)
	fixEdit(t, dir, "seed.yaml", "incidents: [spike]", "incidents: [spike, blip]")
	rc, out := fixRun(runTest, "--dir", dir, "--base", fixBase)
	if rc != 2 {
		t.Fatalf("exit %d, want 2:\n%s", rc, out)
	}
	mustHave(t, out, filepath.Join(dir, "incidents", "blip.yaml")+":6", "disturbance is only for incidents with no fire expectation")
	if strings.Contains(out, "[disturbance]") {
		t.Errorf("an incident with fire expectations must not be counted as a disturbance:\n%s", out)
	}
}
