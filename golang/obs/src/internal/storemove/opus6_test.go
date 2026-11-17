package storemove

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// Opus round-6 review at be665f6, ported. "Old" = the six round-4 bypasses re-expressed
// for the redesign; each must be REFUSED (or behave safely). "New" = attacks on the new
// surface. N1 and N2 were holes (their reviewer versions passed while the hole was open);
// they are inverted here and now require the refusal.

// Old B1: new StatefulSet whose claim name equals the single's claim (single obs-tempo,
// claim storage-obs-tempo-0; new sts "tempo" with template storage-obs).
func TestOpus6OldB1ClaimOfSingle(t *testing.T) {
	live := strings.ReplaceAll(clusterLive("tempo"), "name: single", "name: obs-tempo")
	live = strings.Replace(live, "metadata: {name: data}", "metadata: {name: storage}", 1)
	f := newFake(t, live, "kind: PersistentVolumeClaim\nmetadata: {name: storage-obs-tempo-0}\n")
	cfg := config()
	cfg.Single = "statefulset/obs-tempo"
	cfg.Scaled = []string{"statefulset/tempo"}
	cfg.RoleWorkloads = map[string][]string{"statefulset/tempo": cfg.RoleLabels}
	body := strings.ReplaceAll(scaledManifest("tempo"), "{name: new}", "{name: tempo}")
	body = strings.Replace(body, "metadata: {name: data}", "metadata: {name: storage-obs}", 1)
	body = strings.ReplaceAll(body, "new-headless", "tempo-headless")
	body = strings.ReplaceAll(body, "new-config", "tempo-config")
	cfg.ScaledManifest = writeManifest(t, body)
	expectRefused(t, move(t, f, "tempo", cfg, false), f.calls, "claim adoption")
}

// Old B2: rollback after a forward that failed at Reconnect never touches the single.
func TestOpus6OldB2RollbackKeepsRunningSingle(t *testing.T) {
	f := fakeFor(t, "tempo")
	m := metricsFor("tempo", false)
	m.counters[received("tempo")] = []float64{12, 13}
	_ = runner(f, m, nil).Run(context.Background(), "tempo", config(), false, false)
	f.calls = nil
	if err := move(t, f, "tempo", config(), true); err != nil {
		t.Fatal(err)
	}
	// Ported: the role relabel of the single's own pods (patch pod/single-N, by name and
	// uid) is the intended rollback of a running single; the workload is never touched.
	for _, c := range f.calls {
		if strings.Contains(c, "single") && !strings.HasPrefix(c, "patch pod/single-") && (strings.HasPrefix(c, "patch") || strings.HasPrefix(c, "scale")) {
			t.Fatalf("single changed: %s", c)
		}
	}
	if n, _ := intField(mapAt(f.objs["statefulset.apps/single"], "spec")["replicas"]); n != 1 {
		t.Fatalf("single replicas %d", n)
	}
}

// Old B3: a re-run with a scaled manifest spelling roles "false" cannot clear live roles
// (Prepare only creates; existing owned objects are never re-applied).
func TestOpus6OldB3RerunFalseRoles(t *testing.T) {
	f := fakeFor(t, "tempo")
	f.fail = func(cmd string) error {
		if strings.HasPrefix(cmd, "exec ") {
			return errors.New("planted")
		}
		return nil
	}
	_ = move(t, f, "tempo", config(), false)
	f.fail, f.calls = nil, nil
	cfg := config()
	r := roles("tempo")
	cfg.ScaledManifest = writeManifest(t, strings.Replace(scaledManifest("tempo"), "labels: {component: new}}", `labels: {component: new, `+r[0]+`: "false", `+r[1]+`: "false"}}`, 1))
	err := move(t, f, "tempo", cfg, false)
	expectRefused(t, err, f.calls, "re-run with other manifest")
}

// Old B4: a typo endpoint cannot be typed at all (derived); a manually added endpoint on the
// live router that obs would derive is refused.
func TestOpus6OldB4ManualEndpoint(t *testing.T) {
	live := strings.Replace(vmLive("victoriametrics"), "-storageNode=vmstorage-0.vmstorage.obs-test.svc.cluster.local:8400", "-storageNode=vmstorage-0.vmstorage.obs-test.svc.cluster.local:8400,"+vmEndpoint("vmstorage-b", 0, 8400), 1)
	f := newFake(t, live)
	expectRefused(t, move(t, f, "victoriametrics", storeConfig("victoriametrics"), false), f.calls, "pre-listed derived endpoint")
}

// Old B5: a Pyroscope single that cannot be converted is refused before step 1.
func TestOpus6OldB5LateCheck(t *testing.T) {
	f := newFake(t, strings.Replace(clusterLive("pyroscope"), "whenScaled: Retain", "whenScaled: Delete", 1))
	expectRefused(t, move(t, f, "pyroscope", storeConfig("pyroscope"), false), f.calls, "Delete retention")
}

// Old B6: two copies now supported (replicationFactor on vminsert only).
func TestOpus6OldB6Copies(t *testing.T) {
	cfg := storeConfig("victoriametrics")
	cfg.Copies, cfg.Storage = 2, []string{"statefulset/vmstorage"}
	cfg.ScaledManifest = writeManifest(t, storageScaled("victoriametrics", true))
	f := fakeFor(t, "victoriametrics")
	if err := move(t, f, "victoriametrics", cfg, false); err != nil {
		t.Fatal(err)
	}
	ins, sel := liveArgs(f, "deployment.apps/vminsert"), liveArgs(f, "deployment.apps/vmselect")
	if !strings.Contains(strings.Join(anyStrings(ins), " "), "-replicationFactor=2") || strings.Contains(strings.Join(anyStrings(sel), " "), "replicationFactor") {
		t.Fatalf("insert %v select %v", ins, sel)
	}
}

func anyStrings(a []any) []string {
	var out []string
	for _, x := range a {
		s, _ := x.(string)
		out = append(out, s)
	}
	return out
}

// New N1 (was a hole): resume skipped the "every part Ready" gate. After a failure at the
// drain, a scaled workload becomes not Ready; the re-run resumes at Reconnect and must
// re-check readiness before anything changes; the single keeps running.
func TestOpus6N1ResumeChecksReadiness(t *testing.T) {
	f := fakeFor(t, "tempo")
	f.fail = func(cmd string) error {
		if strings.HasPrefix(cmd, "exec ") {
			return errors.New("planted")
		}
		return nil
	}
	_ = move(t, f, "tempo", config(), false)
	f.fail, f.calls = nil, nil
	f.notReady["statefulset.apps/new"] = true
	expectRefused(t, move(t, f, "tempo", config(), false), f.calls, "resume while the scaled form is not Ready")
	if n, _ := intField(mapAt(f.objs["statefulset.apps/single"], "spec")["replicas"]); n != 1 {
		t.Fatalf("single stopped while the scaled form is not Ready: replicas %d", n)
	}
	if len(callsWith(f.calls, "rollout status statefulset.apps/new")) == 0 {
		t.Fatalf("readiness never checked: %v", f.calls)
	}
	// Ready again: the same re-run resumes and completes.
	delete(f.notReady, "statefulset.apps/new")
	f.calls = nil
	if err := move(t, f, "tempo", config(), false); err != nil {
		t.Fatal(err)
	}
}

// New N2 (was a hole): a self-consistent tampered inverse that only changes a free value
// (the single's replicas 1 -> 5) passed verifyInverse; the rollback started 5 independent
// target=all singles. Now refused before any change; the single stays at 0.
func TestOpus6N2TamperedReplicasRefused(t *testing.T) {
	f := fakeFor(t, "tempo")
	if err := move(t, f, "tempo", config(), false); err != nil {
		t.Fatal(err)
	}
	ann := mapAt(f.objs["statefulset.apps/single"], "metadata", "annotations")
	var ops []op
	if err := json.Unmarshal([]byte(ann[inverseAnnotation].(string)), &ops); err != nil {
		t.Fatal(err)
	}
	for i := range ops {
		if ops[i].Op == "replace" && ops[i].Path == "/spec/replicas" {
			ops[i].Value = 5
		}
	}
	b, _ := json.Marshal(ops)
	ann[inverseAnnotation] = string(b)
	f.calls = nil
	expectRefused(t, move(t, f, "tempo", config(), true), f.calls, "tampered inverse replicas 1 -> 5")
	if n, _ := intField(mapAt(f.objs["statefulset.apps/single"], "spec")["replicas"]); n != 0 {
		t.Fatalf("single restored at replicas %d", n)
	}
}

// New N3: index drift. An arg inserted before -storageNode after the move: the stored
// inverse's test ops no longer match; refused before any change (fail closed).
func TestOpus6N3IndexDriftRefused(t *testing.T) {
	f := fakeFor(t, "victoriametrics")
	if err := move(t, f, "victoriametrics", storeConfig("victoriametrics"), false); err != nil {
		t.Fatal(err)
	}
	cs := mapAt(f.objs["deployment.apps/vminsert"], "spec", "template", "spec")["containers"].([]any)
	c := cs[0].(map[string]any)
	c["args"] = append([]any{"-loggerLevel=INFO"}, c["args"].([]any)...)
	f.calls = nil
	expectRefused(t, move(t, f, "victoriametrics", storeConfig("victoriametrics"), true), f.calls, "index drift")
}

// New N4: check-to-apply race: the router changes between Check and the patch; the
// test ops make the server refuse it; nothing lands.
func TestOpus6N4RaceRouterPatch(t *testing.T) {
	f := fakeFor(t, "victoriametrics")
	f.beforePatch = func(res string) {
		if res == "deployment.apps/vminsert" {
			cs := mapAt(f.objs[res], "spec", "template", "spec")["containers"].([]any)
			c := cs[0].(map[string]any)
			c["args"] = append([]any{"-x=1"}, c["args"].([]any)...)
		}
	}
	err := move(t, f, "victoriametrics", storeConfig("victoriametrics"), false)
	if err == nil {
		t.Fatal("race accepted")
	}
	if inv, _ := storedInverse(f.objs["deployment.apps/vminsert"]); inv != nil {
		t.Fatal("patch landed")
	}
	t.Log(err)
}

// New N5: re-run after a partial create (the StatefulSet create failed, the Service and
// ConfigMap landed). The re-run creates only the absent object.
func TestOpus6N5PartialCreateRerun(t *testing.T) {
	f := fakeFor(t, "tempo")
	first := true
	f.fail = func(cmd string) error {
		if strings.HasPrefix(cmd, "create ") && first {
			first = false
			// land the supporting objects only
			b := f.calls
			_ = b
			return nil
		}
		return nil
	}
	// Simulate partial create: pre-create the Service and ConfigMap as obs would have.
	objs, err := parseManifest(config().ScaledManifest, "obs-test")
	if err != nil {
		t.Fatal(err)
	}
	var partial []object
	for _, o := range objs {
		if o.Kind != "StatefulSet" {
			partial = append(partial, o)
		}
	}
	file, err := createFile(config(), "tempo", partial)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.run(context.Background(), "--context", "fixture", "--namespace", "obs-test", "create", "-f", file); err != nil {
		t.Fatal(err)
	}
	f.calls, f.created = nil, nil
	if err := move(t, f, "tempo", config(), false); err != nil {
		t.Fatal(err)
	}
	if len(f.created) != 1 || strings.Contains(f.created[0], "kind: Service") || !strings.Contains(f.created[0], "kind: StatefulSet") {
		t.Fatalf("re-run created %v", f.created)
	}
	// A changed manifest for an already-created object is refused.
	f2 := fakeFor(t, "tempo")
	if _, err := f2.run(context.Background(), "--context", "fixture", "--namespace", "obs-test", "create", "-f", file); err != nil {
		t.Fatal(err)
	}
	f2.calls = nil
	cfg := config()
	cfg.ScaledManifest = writeManifest(t, strings.Replace(scaledManifest("tempo"), `target: scalable`, `target: other`, 1))
	expectRefused(t, move(t, f2, "tempo", cfg, false), f2.calls, "changed created object")
}

// G4 isolation: a stable Service selecting BOTH role labels (n=2) must be refused; after the
// flip a write-only pod would never match it (empty Service).
func TestOpus6G4ServiceSelectsTwoRoles(t *testing.T) {
	r := roles("tempo")
	live := strings.Replace(clusterLive("tempo"), `spec: {selector: {`+r[1]+`: "true"}`, `spec: {selector: {`+r[1]+`: "true", `+r[0]+`: "true"}`, 1)
	if live == clusterLive("tempo") {
		t.Fatal("fixture")
	}
	f := newFake(t, live)
	expectRefused(t, move(t, f, "tempo", config(), false), f.calls, "Service selects two roles")
}
