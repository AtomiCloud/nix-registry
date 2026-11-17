package storemove

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// Round 6 (Opus + GPT reviews of be665f6): regression tests beyond the ported reviewer
// repros (opus6_test.go, reviewer6_test.go). Each names the item it covers.

// O1, storage stores: a resume after a failure at Writes re-checks the new storage
// nodes (and routers, storage) Ready before anything else.
func TestRound6ResumeReadyStorage(t *testing.T) {
	f := fakeFor(t, "victoriametrics")
	m := metricsFor("victoriametrics", false)
	m.counters[received("victoriametrics")] = []float64{12, 12} // Writes fails: not rising
	if err := runner(f, m, nil).Run(context.Background(), "victoriametrics", storeConfig("victoriametrics"), false, false); err == nil || !strings.Contains(err.Error(), "step 3") {
		t.Fatalf("want a step 3 failure, got %v", err)
	}
	f.calls = nil
	f.notReady["statefulset.apps/vmstorage-b"] = true
	expectRefused(t, move(t, f, "victoriametrics", storeConfig("victoriametrics"), false), f.calls, "resume while a new storage node is not Ready")
	delete(f.notReady, "statefulset.apps/vmstorage-b")
	f.calls = nil
	if err := move(t, f, "victoriametrics", storeConfig("victoriametrics"), false); err != nil {
		t.Fatal(err)
	}
	if len(callsWith(f.calls, "rollout status statefulset.apps/vmstorage-b")) == 0 {
		t.Fatalf("resume did not re-check readiness: %v", f.calls)
	}
}

const compactorLive = `apiVersion: apps/v1
kind: Deployment
metadata: {name: compactor}
spec:
  replicas: 1
  selector: {matchLabels: {app: compactor}}
  template:
    metadata: {labels: {app: compactor}}
    spec: {containers: [{name: compactor, image: "tempo:1", args: ["-target=compactor"]}]}
`

func compactorConfig() Config {
	c := config()
	c.Compactor = "deployment/compactor"
	c.StopCompaction, c.ResumeCompaction = nil, nil
	return c
}

// O3+G1, separate compactor: replicas N -> 0 is many-to-one, so only the recorded
// origin digest can tell the restored count. A tampered inverse (1 -> 7) is refused
// before any change; the untampered inverse restores exactly 1.
func TestRound6CompactorReplicasTamper(t *testing.T) {
	for _, tamper := range []bool{false, true} {
		f := newFake(t, clusterLive("tempo"), compactorLive)
		if err := move(t, f, "tempo", compactorConfig(), false); err != nil {
			t.Fatal(err)
		}
		res := "deployment.apps/compactor"
		if n, _ := intField(mapAt(f.objs[res], "spec")["replicas"]); n != 0 {
			t.Fatalf("compactor not stopped: %d", n)
		}
		if tamper {
			ann := mapAt(f.objs[res], "metadata", "annotations")
			var ops []op
			if err := json.Unmarshal([]byte(ann[inverseAnnotation].(string)), &ops); err != nil {
				t.Fatal(err)
			}
			for i := range ops {
				if ops[i].Op == "replace" && ops[i].Path == "/spec/replicas" {
					ops[i].Value = 7
				}
			}
			b, _ := json.Marshal(ops)
			ann[inverseAnnotation] = string(b)
		}
		f.calls = nil
		err := move(t, f, "tempo", compactorConfig(), true)
		n, _ := intField(mapAt(f.objs[res], "spec")["replicas"])
		if tamper {
			expectRefused(t, err, f.calls, "compactor inverse replicas 1 -> 7")
			if n != 0 {
				t.Fatalf("tampered compactor restored at %d", n)
			}
			continue
		}
		if err != nil || n != 1 {
			t.Fatalf("untampered rollback: err %v replicas %d", err, n)
		}
	}
}

// O3: a single runs exactly one replica; obs refuses to stop or convert anything else,
// so the restored count (1) never depends on the stored inverse.
func TestRound6SingleMustRunOneReplica(t *testing.T) {
	for _, store := range []string{"tempo", "pyroscope"} {
		f := newFake(t, strings.Replace(clusterLive(store), "  replicas: 1\n  serviceName: single-headless", "  replicas: 3\n  serviceName: single-headless", 1))
		expectRefused(t, move(t, f, store, storeConfig(store), false), f.calls, store+" single with 3 replicas")
	}
}

// Q: two copies give vmselect exactly one -dedup.minScrapeInterval=1ms (pinned v1.156.0
// docs: required on vmselect when replication is enabled); the rollback removes it; a
// vmselect with another dedup value is refused; one with exactly 1ms is left as is.
func TestRound6VMSelectDedupTwoCopies(t *testing.T) {
	cfg := storeConfig("victoriametrics")
	cfg.Copies, cfg.Storage = 2, []string{"statefulset/vmstorage"}
	cfg.ScaledManifest = writeManifest(t, storageScaled("victoriametrics", true))
	f := fakeFor(t, "victoriametrics")
	before := f.snapshot()
	if err := move(t, f, "victoriametrics", cfg, false); err != nil {
		t.Fatal(err)
	}
	sel := strings.Join(anyStrings(liveArgs(f, "deployment.apps/vmselect")), " ")
	if strings.Count(sel, "-dedup.minScrapeInterval=1ms") != 1 || strings.Contains(sel, "replicationFactor") {
		t.Fatalf("vmselect args %s", sel)
	}
	if ins := strings.Join(anyStrings(liveArgs(f, "deployment.apps/vminsert")), " "); strings.Contains(ins, "dedup") {
		t.Fatalf("vminsert args %s", ins)
	}
	if err := move(t, f, "victoriametrics", cfg, true); err != nil {
		t.Fatal(err)
	}
	for _, res := range []string{"deployment.apps/vmselect", "deployment.apps/vminsert", "statefulset.apps/vmstorage"} {
		if d := firstDiff(before[res], f.snapshot()[res]); d != "" {
			t.Fatalf("%s not restored at %s", res, d)
		}
	}
	other := strings.Replace(vmLive("victoriametrics"), `"-search.maxConcurrentRequests=4"`, `"-search.maxConcurrentRequests=4", "-dedup.minScrapeInterval=5ms"`, 1)
	f = newFake(t, other)
	expectRefused(t, move(t, f, "victoriametrics", cfg, false), f.calls, "vmselect dedup 5ms")
	same := strings.Replace(vmLive("victoriametrics"), `"-search.maxConcurrentRequests=4"`, `"-search.maxConcurrentRequests=4", "-dedup.minScrapeInterval=1ms"`, 1)
	f = newFake(t, same)
	if err := move(t, f, "victoriametrics", cfg, false); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(strings.Join(anyStrings(liveArgs(f, "deployment.apps/vmselect")), " "), "dedup"); n != 1 {
		t.Fatalf("vmselect dedup flags %d", n)
	}
}

// G3: a pod replaced by a namesake between obs's list and its patch (another uid) is
// refused by the uid test op; nothing lands on it.
func TestRound6PodNamesakeRefused(t *testing.T) {
	f := fakeFor(t, "tempo")
	f.beforePatch = func(res string) {
		if res == "pod/new-0" {
			mapAt(f.pods["new-0"], "metadata")["uid"] = "uid-namesake"
		}
	}
	err := move(t, f, "tempo", config(), false)
	if err == nil || !strings.Contains(err.Error(), "test /metadata/uid") {
		t.Fatalf("namesake pod patched: %v", err)
	}
	for _, r := range roles("tempo") {
		if v := f.podLabels("new-0")[r]; v == "true" {
			t.Fatalf("namesake got %s=true", r)
		}
	}
}

// G3, Deployment role workload: a late pod controlled by a ReplicaSet the Deployment
// does not own is refused, never relabelled.
func TestRound6ForeignReplicaSetPod(t *testing.T) {
	cfg := config()
	cfg.Scaled = []string{"deployment/distributor", "deployment/query"}
	cfg.RoleWorkloads = map[string][]string{"deployment/distributor": {"obs.diene/tempo-write"}, "deployment/query": {"obs.diene/tempo-read"}}
	wl := func(name string) string {
		return "apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: " + name + "}\nspec: {replicas: 1, selector: {matchLabels: {component: " + name + "}}, template: {metadata: {labels: {component: " + name + "}}, spec: {containers: [{name: c, image: x}]}}}\n"
	}
	cfg.ScaledManifest = writeManifest(t, wl("distributor")+"---\n"+wl("query"))
	f := fakeFor(t, "tempo")
	done := false
	f.fail = func(cmd string) error {
		if !done && strings.HasPrefix(cmd, "rollout status deployment.apps/query") {
			done = true
			f.pods["query-other"] = map[string]any{"metadata": map[string]any{"name": "query-other", "uid": "uid-pod-other", "labels": map[string]any{"component": "query"},
				"ownerReferences": []any{map[string]any{"kind": "ReplicaSet", "name": "other", "uid": "uid-rs-other", "controller": true}}}}
		}
		return nil
	}
	if err := move(t, f, "tempo", cfg, false); err == nil || !strings.Contains(err.Error(), "query-other") {
		t.Fatalf("foreign ReplicaSet pod accepted: %v", err)
	}
	if v, ok := f.podLabels("query-other")["obs.diene/tempo-read"]; ok {
		t.Fatalf("foreign pod relabelled: %s", v)
	}
	if v := f.podLabels("query-0")["obs.diene/tempo-read"]; v == "true" {
		t.Fatal("roles moved although the route was refused")
	}
}

// Item 3: a stable Service selector changed after the up-front check (an extra
// single-only term) is caught by the re-read at Route, before any role changes.
func TestRound6StableServiceChangedBeforeRoute(t *testing.T) {
	f := fakeFor(t, "tempo")
	f.fail = func(cmd string) error {
		if strings.HasPrefix(cmd, "create ") {
			stringSel := mapAt(f.objs["service/tempo-query"], "spec", "selector")
			stringSel["component"] = "single"
		}
		return nil
	}
	err := move(t, f, "tempo", config(), false)
	if err == nil || !strings.Contains(err.Error(), "tempo-query") {
		t.Fatalf("changed stable Service accepted: %v", err)
	}
	if c := callsWith(f.calls, "patch pod/"); len(c) > 0 {
		t.Fatalf("roles changed: %v", c)
	}
	// A selector term every serving pod carries (single and scaled) is fine.
	shared := strings.ReplaceAll(clusterLive("tempo"), "spec: {selector: {obs.diene/tempo-", "spec: {selector: {app.kubernetes.io/part-of: tempo, obs.diene/tempo-")
	shared = strings.Replace(shared, "metadata: {labels: {component: single,", "metadata: {labels: {app.kubernetes.io/part-of: tempo, component: single,", 1)
	cfg := config()
	cfg.ScaledManifest = writeManifest(t, strings.Replace(scaledManifest("tempo"), "metadata: {labels: {component: new}}", "metadata: {labels: {app.kubernetes.io/part-of: tempo, component: new}}", 1))
	f = newFake(t, shared)
	if err := move(t, f, "tempo", cfg, false); err != nil {
		t.Fatal(err)
	}
}

// Items 1, 5 (G4, G5): every patch obs sends to a workload starts with tests of the
// object's uid and its whole spec; every pod patch with its uid and owner references.
func TestRound6EveryPatchPinsTheReadObject(t *testing.T) {
	for _, store := range []string{"tempo", "pyroscope", "victorialogs", "victoriametrics"} {
		for _, rollback := range []bool{false, true} {
			f := fakeFor(t, store)
			if rollback {
				if err := move(t, f, store, storeConfig(store), false); err != nil {
					t.Fatal(err)
				}
				f.calls = nil
			}
			if err := move(t, f, store, storeConfig(store), rollback); err != nil {
				t.Fatal(err)
			}
			n := 0
			for _, c := range callsWith(f.calls, "patch ") {
				_, body, _ := strings.Cut(c, " -p ")
				var ops []op
				if err := json.Unmarshal([]byte(body), &ops); err != nil {
					t.Fatal(err)
				}
				want := []string{"/metadata/uid", "/spec"}
				if strings.HasPrefix(c, "patch pod/") {
					want = []string{"/metadata/uid", "/metadata/ownerReferences"}
				}
				for i, p := range want {
					if len(ops) <= i || ops[i].Op != "test" || ops[i].Path != p {
						t.Fatalf("%s rollback=%v: %s does not start with test %v", store, rollback, c, want)
					}
				}
				n++
			}
			if n == 0 {
				t.Fatalf("%s rollback=%v: no patch sent", store, rollback)
			}
		}
	}
}

// G5 re-read: the retention policy turns Delete during the drain (after the stop of
// compaction, before the stop of the single); the re-read refuses and the single runs.
func TestRound6RetentionDeleteDuringDrain(t *testing.T) {
	f := fakeFor(t, "tempo")
	f.fail = func(cmd string) error {
		if strings.HasPrefix(cmd, "exec ") {
			mapAt(f.objs["statefulset.apps/single"], "spec", "persistentVolumeClaimRetentionPolicy")["whenScaled"] = "Delete"
		}
		return nil
	}
	err := move(t, f, "tempo", config(), false)
	if err == nil || !strings.Contains(err.Error(), "whenScaled=Delete") {
		t.Fatalf("stopped a single with live whenScaled Delete: %v", err)
	}
	if n, _ := intField(mapAt(f.objs["statefulset.apps/single"], "spec")["replicas"]); n != 1 {
		t.Fatalf("single replicas %d", n)
	}
}

// Item 5: a concurrent -dedup.minScrapeInterval=5ms appended to the existing vmstorage
// between obs's read and its patch is refused (whole spec pinned).
func TestRound6DedupRaceOnStorage(t *testing.T) {
	cfg := storeConfig("victoriametrics")
	cfg.Copies, cfg.Storage = 2, []string{"statefulset/vmstorage"}
	cfg.ScaledManifest = writeManifest(t, storageScaled("victoriametrics", true))
	f := fakeFor(t, "victoriametrics")
	f.beforePatch = func(res string) {
		if res != "statefulset.apps/vmstorage" {
			return
		}
		f.beforePatch = nil
		cm := mapAt(f.objs[res], "spec", "template", "spec")["containers"].([]any)[0].(map[string]any)
		cm["args"] = append(cm["args"].([]any), "-dedup.minScrapeInterval=5ms")
	}
	err := move(t, f, "victoriametrics", cfg, false)
	if err == nil || !strings.Contains(err.Error(), "test /spec") {
		t.Fatalf("dedup race accepted: %v; args %v", err, liveArgs(f, "statefulset.apps/vmstorage"))
	}
	if inv, _ := storedInverse(f.objs["statefulset.apps/vmstorage"]); inv != nil {
		t.Fatal("patch landed")
	}
}

// G2: spec.ordinals.start is honoured (claims, pods, endpoints); a malformed ordinals is
// refused statically.
func TestRound6Ordinals(t *testing.T) {
	withStart := strings.Replace(scaledManifest("tempo"), "  replicas: 2\n", "  replicas: 2\n  ordinals: {start: 20}\n", 1)
	cfg := config()
	cfg.ScaledManifest = writeManifest(t, withStart)
	if got := claimNames(object{Kind: "StatefulSet", Name: "new", Raw: mustParse(t, cfg)}); strings.Join(got, ",") != "data-new-20,data-new-21" {
		t.Fatalf("claims %v", got)
	}
	f := fakeFor(t, "tempo")
	if err := move(t, f, "tempo", cfg, false); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"new-20", "new-21"} {
		if f.podLabels(p)["obs.diene/tempo-read"] != "true" {
			t.Fatalf("%s roles %v", p, f.podLabels(p))
		}
	}
	for name, ord := range map[string]string{"negative": "{start: -1}", "unknown key": "{start: 1, end: 3}", "not integer": "{start: x}", "not object": "3"} {
		cfg.ScaledManifest = writeManifest(t, strings.Replace(scaledManifest("tempo"), "  replicas: 2\n", "  replicas: 2\n  ordinals: "+ord+"\n", 1))
		if _, err := validateScaledManifest(cfg, "tempo"); err == nil || !strings.Contains(err.Error(), "ordinals") {
			t.Fatalf("%s: %v", name, err)
		}
	}
	dep := "apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: new}\nspec: {replicas: 1, ordinals: {start: 1}, selector: {matchLabels: {component: new}}, template: {metadata: {labels: {component: new}}, spec: {containers: [{name: c, image: x}]}}}\n"
	cfg.ScaledManifest = writeManifest(t, dep)
	cfg.Scaled = []string{"deployment/new"}
	cfg.RoleWorkloads = map[string][]string{"deployment/new": cfg.RoleLabels}
	if _, err := validateScaledManifest(cfg, "tempo"); err == nil || !strings.Contains(err.Error(), "ordinals") {
		t.Fatalf("Deployment ordinals: %v", err)
	}
}

func mustParse(t *testing.T, cfg Config) map[string]any {
	t.Helper()
	objs, err := validateScaledManifest(cfg, "tempo")
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range objs {
		if o.Kind == "StatefulSet" {
			return o.Raw
		}
	}
	t.Fatal("no StatefulSet")
	return nil
}

// The fake Deployment controller bumps deployment.kubernetes.io/revision after obs
// patches a router (as the real one does); the rollback still verifies (the stored
// inverse never names that annotation) and restores.
func TestRound6DeploymentRevisionBookkeeping(t *testing.T) {
	f := fakeFor(t, "victorialogs")
	if err := move(t, f, "victorialogs", storeConfig("victorialogs"), false); err != nil {
		t.Fatal(err)
	}
	if rev := mapAt(f.objs["deployment.apps/vlinsert"], "metadata", "annotations")["deployment.kubernetes.io/revision"]; rev != "2" {
		t.Fatalf("fake controller revision %v", rev)
	}
	if err := move(t, f, "victorialogs", storeConfig("victorialogs"), true); err != nil {
		t.Fatal(err)
	}
}
