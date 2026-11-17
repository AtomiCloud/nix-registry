package storemove

// GPT (henna) verification of round 6 at 5a8f3b0, ported: an empty-valued selector term
// still requires the label key to exist (Kubernetes label selector semantics).
import (
	"strings"
	"testing"
)

// Item GPT3: a selector for an empty label value still requires that key to exist.
// The single has tenant=""; scaled pods have no tenant label. The unchanged Service
// initially routes to the single and would become empty after the single is stopped.
func TestVerifyRound6EmptySelectorTerm(t *testing.T) {
	live := strings.ReplaceAll(clusterLive("tempo"), "spec: {selector: {obs.diene/tempo-", "spec: {selector: {tenant: \"\", obs.diene/tempo-")
	live = strings.Replace(live, "metadata: {labels: {component: single,", "metadata: {labels: {tenant: \"\", component: single,", 1)
	f := newFake(t, live)
	for _, res := range []string{"service/tempo-query", "service/tempo-distributor"} {
		sel := stringMap(mapAt(f.objs[res], "spec")["selector"])
		singleLabels := stringMap(mapAt(f.objs["statefulset.apps/single"], "spec", "template", "metadata")["labels"])
		if v, ok := singleLabels["tenant"]; !ok || v != sel["tenant"] {
			t.Fatal("fixture: single must match tenant=empty selector")
		}
	}
	err := move(t, f, "tempo", config(), false)
	if err != nil {
		return
	}
	for _, res := range []string{"service/tempo-query", "service/tempo-distributor"} {
		sel := stringMap(mapAt(f.objs[res], "spec")["selector"])
		matched := 0
		for _, p := range f.pods {
			labels := stringMap(mapAt(p, "metadata")["labels"])
			match := true
			for k, v := range sel {
				if got, exists := labels[k]; !exists || got != v {
					match = false
				}
			}
			if match {
				matched++
			}
		}
		if matched == 0 {
			t.Errorf("successful move stopped single; stable %s tenant=empty selector matches zero pods (missing label is not empty label)", res)
		}
	}
}

// The same missing-vs-empty rule on the single's side: a stable Service term tenant=""
// that the single's template lacks (the scaled template carries it) is refused up front.
func TestVerifyRound6EmptySelectorTermSingleSide(t *testing.T) {
	live := strings.ReplaceAll(clusterLive("tempo"), "spec: {selector: {obs.diene/tempo-", "spec: {selector: {tenant: \"\", obs.diene/tempo-")
	cfg := config()
	cfg.ScaledManifest = writeManifest(t, strings.Replace(scaledManifest("tempo"), "metadata: {labels: {component: new}}", "metadata: {labels: {tenant: \"\", component: new}}", 1))
	f := newFake(t, live)
	err := move(t, f, "tempo", cfg, false)
	expectRefused(t, err, f.calls, "stable Service term tenant=\"\" absent from the single's template")
	if !strings.Contains(err.Error(), "tenant") {
		t.Fatalf("refusal does not name the term: %v", err)
	}
	// Control: both templates carry tenant="": accepted.
	f = newFake(t, strings.Replace(live, "metadata: {labels: {component: single,", "metadata: {labels: {tenant: \"\", component: single,", 1))
	if err := move(t, f, "tempo", cfg, false); err != nil {
		t.Fatal(err)
	}
}

// The new storage node's headless Service: a selector term tenant="" the pods do not
// carry selects none of them (the derived endpoints would not resolve); refused
// statically. Control: pods carrying tenant="" are accepted.
func TestVerifyRound6EmptyStorageServiceTerm(t *testing.T) {
	cfg := storeConfig("victoriametrics")
	body := strings.Replace(storageScaled("victoriametrics", false), "spec: {clusterIP: None, selector: {app: vmstorage-b}}", "spec: {clusterIP: None, selector: {app: vmstorage-b, tenant: \"\"}}", 1)
	cfg.ScaledManifest = writeManifest(t, body)
	if _, err := validateScaledManifest(cfg, "victoriametrics"); err == nil || !strings.Contains(err.Error(), "tenant") {
		t.Fatalf("Service term tenant=\"\" absent from the pods accepted: %v", err)
	}
	cfg.ScaledManifest = writeManifest(t, strings.Replace(body, "metadata: {labels: {app: vmstorage-b}}", "metadata: {labels: {app: vmstorage-b, tenant: \"\"}}", 1))
	if _, err := validateScaledManifest(cfg, "victoriametrics"); err != nil {
		t.Fatal(err)
	}
}
