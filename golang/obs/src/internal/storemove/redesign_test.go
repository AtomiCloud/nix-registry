package storemove

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Round 5 redesign: new objects are created (create-only), existing objects change only
// through JSON patches obs builds from the live object (test ops + verified stored
// inverse). Every test drives the real Kubernetes implementation against fakeKube.

func metricsFor(store string, rollback bool) *fakeMetrics {
	m := &fakeMetrics{counters: map[string][]float64{received(store): {12, 12}, "tempo_live_store_blocks_completed_total": {5, 6}, "tempo_live_store_local_blocks_flushed_total": {5, 6}, "vl_insert_remote_send_errors_total": {0, 0}}}
	if storage(store) && !rollback {
		m.counters[received(store)] = []float64{12, 13, 14, 15}
	}
	return m
}
func runner(f *fakeKube, m *fakeMetrics, out *bytes.Buffer) Runner {
	r := Runner{Cluster: f.kube(), Metrics: m, Pause: func(context.Context, time.Duration) error { return nil }, Now: func() time.Time { return time.Unix(1700000000, 0) }}
	if out != nil {
		r.Out = out
	}
	return r
}
func move(t *testing.T, f *fakeKube, store string, cfg Config, rollback bool) error {
	t.Helper()
	return runner(f, metricsFor(store, rollback), nil).Run(context.Background(), store, cfg, rollback, false)
}
func liveArgs(f *fakeKube, res string) []any {
	cs, _ := mapAt(f.objs[res], "spec", "template", "spec")["containers"].([]any)
	c, _ := cs[0].(map[string]any)
	a, _ := c["args"].([]any)
	return a
}
func callsWith(calls []string, parts ...string) []string {
	var out []string
	for _, c := range calls {
		all := true
		for _, p := range parts {
			all = all && strings.Contains(c, p)
		}
		if all {
			out = append(out, c)
		}
	}
	return out
}
func vmEndpoint(sts string, i, port int) string {
	return fmt.Sprintf("%s-%d.%s.obs-test.svc.cluster.local:%d", sts, i, sts, port)
}

// The full move and its rollback for every store: the rollback restores every existing
// object exactly (field for field, including tolerations, quantities and every arg), the
// new objects and their claims stay, and no `apply` or `scale` is ever used.
func TestRedesignForwardThenRollbackRestoresExistingObjects(t *testing.T) {
	for _, store := range []string{"tempo", "pyroscope", "victorialogs", "victoriametrics"} {
		t.Run(store, func(t *testing.T) {
			f := fakeFor(t, store)
			before := f.snapshot()
			cfg := storeConfig(store)
			if err := move(t, f, store, cfg, false); err != nil {
				t.Fatal(err)
			}
			switch store {
			case "tempo":
				s := f.objs["statefulset.apps/single"]
				if n, _ := intField(mapAt(s, "spec")["replicas"]); n != 0 {
					t.Fatalf("single not stopped: %v", n)
				}
				if f.podLabels("new-0")["obs.diene/tempo-read"] != "true" || f.podLabels("new-1")["obs.diene/tempo-write"] != "true" {
					t.Fatalf("scaled pods without roles: %v", f.podLabels("new-0"))
				}
			case "pyroscope":
				if got := liveArgs(f, "statefulset.apps/single")[0]; got != "-target=metastore" {
					t.Fatalf("metastore target %v", got)
				}
				if f.podLabels("single-0")["obs.diene/pyroscope-write"] != "false" {
					t.Fatalf("metastore pod keeps write role: %v", f.podLabels("single-0"))
				}
			case "victoriametrics":
				want := "-storageNode=vmstorage-0.vmstorage.obs-test.svc.cluster.local:8400," + vmEndpoint("vmstorage-b", 0, 8400) + "," + vmEndpoint("vmstorage-b", 1, 8400)
				if got := liveArgs(f, "deployment.apps/vminsert"); !reflect.DeepEqual(got, []any{"-httpListenAddr=:8480", "-memory.allowedPercent", "60", want}) {
					t.Fatalf("vminsert args %v", got)
				}
			}
			moved := f.snapshot()
			if err := move(t, f, store, cfg, true); err != nil {
				t.Fatal(err)
			}
			after := f.snapshot()
			for res, obj := range before {
				if !reflect.DeepEqual(after[res], obj) {
					t.Errorf("rollback did not restore %s exactly (first difference %s)", res, firstDiff(after[res], obj))
				}
			}
			for res := range moved {
				if _, existed := before[res]; !existed {
					if _, kept := after[res]; !kept {
						t.Errorf("rollback removed new object %s (new pods and claims are retained)", res)
					}
				}
			}
			if !storage(store) {
				r := roles(store)
				if l := f.podLabels("single-0"); l[r[0]] != "true" || l[r[1]] != "true" {
					t.Fatalf("single pod not serving after rollback: %v", l)
				}
				if l := f.podLabels("new-0"); l[r[0]] != "false" || l[r[1]] != "false" {
					t.Fatalf("scaled pod still serving after rollback: %v", l)
				}
			}
			for _, c := range f.calls {
				if strings.HasPrefix(c, "apply ") || strings.HasPrefix(c, "scale ") {
					t.Fatalf("hand-written apply or bare scale used: %s", c)
				}
			}
		})
	}
}

// Brief item 2: every patch to an existing object starts with test operations. GPT round
// 5 items 1 and 2 (an omitted field of an atomic list, quantity spellings) cannot occur:
// the patches name only the changed paths, so tolerations and resources are never
// written (the forward/rollback test above also proves them byte-identical afterwards).
func TestGPT5PatchesTouchOnlyDeclaredPaths(t *testing.T) {
	for _, store := range []string{"tempo", "pyroscope"} {
		t.Run(store, func(t *testing.T) {
			f := fakeFor(t, store)
			cfg := storeConfig(store).normalized()
			fwd, err := singleBuilder(store, cfg)(f.objs["statefulset.apps/single"])
			if err != nil {
				t.Fatal(err)
			}
			if fwd[0].Op != "test" {
				t.Fatalf("patch does not start with a test op: %v", fwd)
			}
			allowed := map[string]bool{"/spec/replicas": true, "/spec/template/spec/containers/0/args/0": true}
			for _, r := range roles(store) {
				allowed[rolePath(r)] = true
			}
			for _, o := range fwd {
				if o.Op != "test" && !allowed[o.Path] {
					t.Fatalf("%s patch writes %s", store, o.Path)
				}
				if strings.Contains(o.Path, "tolerations") || strings.Contains(o.Path, "resources") {
					t.Fatalf("patch names %s", o.Path)
				}
			}
		})
	}
}

// GPT round 5 item 3: an unrelated value flag spelled with a separate value
// (`-memory.allowedPercent 60`) in front of -storageNode is read as a value, not a
// positional argument; the move adds the endpoints and keeps both arguments.
func TestGPT5OtherSeparateValueFlag(t *testing.T) {
	f := fakeFor(t, "victoriametrics")
	if err := move(t, f, "victoriametrics", storeConfig("victoriametrics"), false); err != nil {
		t.Fatalf("unchanged valid separate-value flag rejected: %v", err)
	}
	if a := liveArgs(f, "deployment.apps/vminsert"); a[1] != "-memory.allowedPercent" || a[2] != "60" {
		t.Fatalf("args changed: %v", a)
	}
}

// GPT round 5 item 4: a live Delete claim retention policy is refused before any change,
// for the forward Tempo stop (scale to zero) as well as Pyroscope, and also when the
// stop step itself is called directly.
func TestGPT5LiveDeleteRetentionRefusedForward(t *testing.T) {
	for _, store := range []string{"tempo", "pyroscope"} {
		for _, key := range []string{"whenScaled", "whenDeleted"} {
			t.Run(store+"/"+key, func(t *testing.T) {
				f := newFake(t, strings.Replace(clusterLive(store), key+": Retain", key+": Delete", 1))
				err := move(t, f, store, storeConfig(store), false)
				expectRefused(t, err, f.calls, "live "+key+" Delete")
				if !strings.Contains(err.Error(), "Delete") {
					t.Fatalf("refusal does not name the policy: %v", err)
				}
			})
		}
	}
	t.Run("stop single step", func(t *testing.T) {
		f := newFake(t, strings.Replace(clusterLive("tempo"), "whenScaled: Retain", "whenScaled: Delete", 1))
		err := f.kube().Execute(context.Background(), StopSingle, "tempo", config())
		expectRefused(t, err, f.calls, "stop single with whenScaled Delete")
	})
	t.Run("control: Retain", func(t *testing.T) {
		if err := move(t, fakeFor(t, "tempo"), "tempo", config(), false); err != nil {
			t.Fatal(err)
		}
	})
}

// ---- Opus round 5 repros (each attack must now fail) ----

// B1 / finding 3: a new StatefulSet whose claim name (<template>-<sts>-<n>) already exists
// would adopt it. Refused before any change, for every replica index.
func TestOpus5B1ClaimAdoptionRefused(t *testing.T) {
	for _, claim := range []string{"data-new-0", "data-new-1"} {
		t.Run(claim, func(t *testing.T) {
			f := newFake(t, clusterLive("tempo"), "kind: PersistentVolumeClaim\nmetadata: {name: "+claim+"}\nspec: {accessModes: [ReadWriteOnce]}\n")
			expectRefused(t, move(t, f, "tempo", config(), false), f.calls, "claim "+claim)
		})
	}
	t.Run("control: data-new-2 is not bound by 2 replicas", func(t *testing.T) {
		f := newFake(t, clusterLive("tempo"), "kind: PersistentVolumeClaim\nmetadata: {name: data-new-2}\n")
		if err := move(t, f, "tempo", config(), false); err != nil {
			t.Fatal(err)
		}
	})
}

// B2 / finding 1: rollback after a forward move that failed before the drain. The single
// is running and undrained: the rollback reads it first and only relabels it; it never
// patches or scales the single.
func TestOpus5B2RollbackNeverStopsRunningSingle(t *testing.T) {
	f := fakeFor(t, "tempo")
	m := metricsFor("tempo", false)
	m.counters[received("tempo")] = []float64{12, 13} // still receiving: Reconnect fails
	if err := runner(f, m, nil).Run(context.Background(), "tempo", config(), false, false); err == nil || !strings.Contains(err.Error(), "step 3") {
		t.Fatalf("want a step 3 failure, got %v", err)
	}
	f.calls = nil
	if err := move(t, f, "tempo", config(), true); err != nil {
		t.Fatal(err)
	}
	if c := callsWith(f.calls, "statefulset.apps/single", "patch"); len(c) > 0 {
		t.Fatalf("rollback changed the running single: %v", c)
	}
	if _, ok := f.pods["single-0"]; !ok {
		t.Fatal("single pod stopped")
	}
	if l := f.podLabels("single-0"); l["obs.diene/tempo-read"] != "true" || l["obs.diene/tempo-write"] != "true" {
		t.Fatalf("single not relabelled: %v", l)
	}
}

// B3 / finding 2: re-running the forward move after it failed at the drain resumes at
// Reconnect (the live state shows Prepare and Route done); it creates nothing and never
// touches the role labels of pods that already serve.
func TestOpus5B3RerunResumesWithoutTouchingServingPods(t *testing.T) {
	f := fakeFor(t, "tempo")
	f.fail = func(cmd string) error {
		if strings.HasPrefix(cmd, "exec ") {
			return errors.New("planted: compaction control unavailable")
		}
		return nil
	}
	if err := move(t, f, "tempo", config(), false); err == nil || !strings.Contains(err.Error(), "step 4") {
		t.Fatalf("want a step 4 failure, got %v", err)
	}
	f.fail, f.calls = nil, nil
	var out bytes.Buffer
	if err := runner(f, metricsFor("tempo", false), &out).Run(context.Background(), "tempo", config(), false, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "2. flip role labels to scaled pods (already live; resuming)") {
		t.Fatalf("did not resume:\n%s", out.String())
	}
	if c := callsWith(f.calls, "create "); len(c) > 0 {
		t.Fatalf("re-run created again: %v", c)
	}
	for _, c := range callsWith(f.calls, "label pods", "component=new") {
		if strings.Contains(c, "=false") {
			t.Fatalf("re-run cleared serving roles: %s", c)
		}
	}
}

// B4 / finding 5: the storage endpoints are derived (StatefulSet name, replicas, headless
// Service, router port, namespace); nothing in the config can type one. A manifest whose
// serviceName, Service selector or ports do not give working endpoints is refused.
func TestOpus5B4EndpointsDerivedNeverTyped(t *testing.T) {
	var out bytes.Buffer
	r := Runner{Out: &out}
	if err := r.Run(context.Background(), "victoriametrics", storeConfig("victoriametrics"), false, true); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{vmEndpoint("vmstorage-b", 0, 8400), vmEndpoint("vmstorage-b", 1, 8400), vmEndpoint("vmstorage-b", 1, 8401)} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("dry run lacks derived endpoint %s:\n%s", want, out.String())
		}
	}
	for _, tc := range []struct{ name, from, to string }{
		{"serviceName typo", "  serviceName: vmstorage-b\n", "  serviceName: vmstorag-b\n"},
		{"Service does not select the pods", "selector: {app: vmstorage-b}}\n", "selector: {app: other}}\n"},
		{"router port not a containerPort", "{containerPort: 8401}", "{containerPort: 8402}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := storageScaled("victoriametrics", false)
			if !strings.Contains(body, tc.from) {
				t.Fatalf("fixture lacks %q", tc.from)
			}
			cfg := storeConfig("victoriametrics")
			cfg.ScaledManifest = writeManifest(t, strings.Replace(body, tc.from, tc.to, 1))
			f := fakeFor(t, "victoriametrics")
			expectRefused(t, move(t, f, "victoriametrics", cfg, false), f.calls, tc.name)
		})
	}
}

// B5 / finding 4: everything is checked before step 1. A single that cannot be converted
// (Pyroscope) or stopped is refused before any label moves.
func TestOpus5B5EverythingCheckedBeforeStep1(t *testing.T) {
	for _, tc := range []struct{ name, store, from, to string }{
		{"pyroscope: -target already metastore (not by obs)", "pyroscope", `"-target=all"`, `"-target=metastore"`},
		{"pyroscope: two -target flags", "pyroscope", `"-target=all"`, `"-target=all", "-target=all"`},
		{"pyroscope: no -target", "pyroscope", `"-target=all", `, ``},
		{"pyroscope: single at replicas 0", "pyroscope", "  replicas: 1\n", "  replicas: 0\n"},
		{"tempo: single template without its roles", "tempo", `obs.diene/tempo-read: "true", obs.diene/tempo-write: "true"`, `obs.diene/tempo-read: "false", obs.diene/tempo-write: "true"`},
		{"tempo: stable Service selects no role", "tempo", `spec: {selector: {obs.diene/tempo-read: "true"}`, `spec: {selector: {app: tempo}`},
		{"tempo: one Alloy replica", "tempo", "  replicas: 2\n  selector: {matchLabels: {app: alloy}}", "  replicas: 1\n  selector: {matchLabels: {app: alloy}}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			live := clusterLive(tc.store)
			if !strings.Contains(live, tc.from) {
				t.Fatalf("fixture lacks %q", tc.from)
			}
			f := newFake(t, strings.Replace(live, tc.from, tc.to, 1))
			expectRefused(t, move(t, f, tc.store, storeConfig(tc.store), false), f.calls, tc.name)
		})
	}
}

// B6 / finding 6: two copies (concept 10.5) put -replicationFactor=2 on vminsert only,
// -dedup.minScrapeInterval=1ms on every vmstorage (existing: patched; new: required in
// the manifest), and the rollback removes both again.
func TestOpus5B6TwoCopies(t *testing.T) {
	cfg := storeConfig("victoriametrics")
	cfg.Copies = 2
	cfg.Storage = []string{"statefulset/vmstorage"}
	cfg.ScaledManifest = writeManifest(t, storageScaled("victoriametrics", true))
	f := fakeFor(t, "victoriametrics")
	before := f.snapshot()
	if err := move(t, f, "victoriametrics", cfg, false); err != nil {
		t.Fatal(err)
	}
	ins := liveArgs(f, "deployment.apps/vminsert")
	if ins[3] != "-replicationFactor=2" || !strings.HasPrefix(ins[4].(string), "-storageNode=") {
		t.Fatalf("vminsert args %v", ins)
	}
	for _, a := range liveArgs(f, "deployment.apps/vmselect") {
		if strings.Contains(a.(string), "replicationFactor") {
			t.Fatalf("vmselect got %v", a)
		}
	}
	if a := liveArgs(f, "statefulset.apps/vmstorage"); a[0] != dedupExact {
		t.Fatalf("existing vmstorage args %v", a)
	}
	if err := move(t, f, "victoriametrics", cfg, true); err != nil {
		t.Fatal(err)
	}
	for res, obj := range before {
		if !reflect.DeepEqual(f.snapshot()[res], obj) {
			t.Fatalf("rollback left %s changed", res)
		}
	}
	t.Run("new vmstorage without dedup", func(t *testing.T) {
		cfg := cfg
		cfg.ScaledManifest = writeManifest(t, storageScaled("victoriametrics", false))
		f := fakeFor(t, "victoriametrics")
		expectRefused(t, move(t, f, "victoriametrics", cfg, false), f.calls, "new vmstorage without dedup")
	})
	t.Run("live -replicationFactor=3", func(t *testing.T) {
		f := newFake(t, strings.Replace(vmLive("victoriametrics"), `"-httpListenAddr=:8480", `, `"-httpListenAddr=:8480", "-replicationFactor=3", `, 1))
		expectRefused(t, move(t, f, "victoriametrics", cfg, false), f.calls, "live replicationFactor 3")
	})
	t.Run("live vmstorage other dedup value", func(t *testing.T) {
		f := newFake(t, strings.Replace(vmLive("victoriametrics"), `["-retentionPeriod=30d", `, `["-dedup.minScrapeInterval=30s", "-retentionPeriod=30d", `, 1))
		expectRefused(t, move(t, f, "victoriametrics", cfg, false), f.calls, "live dedup 30s")
	})
	t.Run("live vmstorage already 1ms: no change, rollback keeps it", func(t *testing.T) {
		live := strings.Replace(vmLive("victoriametrics"), `["-retentionPeriod=30d", `, `["-dedup.minScrapeInterval=1ms", "-retentionPeriod=30d", `, 1)
		f := newFake(t, live)
		if err := move(t, f, "victoriametrics", cfg, false); err != nil {
			t.Fatal(err)
		}
		if c := callsWith(f.calls, "patch statefulset.apps/vmstorage "); len(c) > 0 {
			t.Fatalf("patched a vmstorage that already had 1ms: %v", c)
		}
		if err := move(t, f, "victoriametrics", cfg, true); err != nil {
			t.Fatal(err)
		}
		if a := liveArgs(f, "statefulset.apps/vmstorage"); a[0] != dedupExact {
			t.Fatalf("rollback removed a pre-existing setting: %v", a)
		}
	})
	t.Run("copies 2 refused for VictoriaLogs", func(t *testing.T) {
		c := storeConfig("victorialogs")
		c.Copies = 2
		if err := c.Validate("victorialogs", false); err == nil {
			t.Fatal("accepted")
		}
	})
}

// ---- test operations, stored inverses ----

// A concurrent change between obs's read and its patch: the test op makes the server
// refuse the whole patch, nothing of it lands.
func TestPatchTestOpRefusesDriftDuringApply(t *testing.T) {
	f := fakeFor(t, "tempo")
	f.beforePatch = func(res string) {
		if res == "statefulset.apps/single" {
			mapAt(f.objs[res], "spec")["replicas"] = float64(3)
		}
	}
	err := move(t, f, "tempo", config(), false)
	if err == nil || !strings.Contains(err.Error(), "test /spec/replicas") {
		t.Fatalf("drifted single patched: %v", err)
	}
	s := f.objs["statefulset.apps/single"]
	if n, _ := intField(mapAt(s, "spec")["replicas"]); n != 3 {
		t.Fatalf("partial patch landed: replicas %d", n)
	}
	if inv, _ := storedInverse(s); inv != nil {
		t.Fatal("inverse stored for a refused patch")
	}
}

// The rollback verifies the stored inverse: a live object that changed after the move,
// a hand-edited annotation, or a config that derives other endpoints are refused before
// any change.
func TestStoredInverseVerified(t *testing.T) {
	setup := func(t *testing.T) *fakeKube {
		f := fakeFor(t, "victoriametrics")
		if err := move(t, f, "victoriametrics", storeConfig("victoriametrics"), false); err != nil {
			t.Fatal(err)
		}
		f.calls = nil
		return f
	}
	t.Run("control: an unrelated field changed after the move; the rollback undoes only obs's change", func(t *testing.T) {
		f := setup(t)
		cs := mapAt(f.objs["deployment.apps/vminsert"], "spec", "template", "spec")["containers"].([]any)
		cs[0].(map[string]any)["image"] = "vm:2"
		if err := move(t, f, "victoriametrics", storeConfig("victoriametrics"), true); err != nil {
			t.Fatal(err)
		}
		if a := liveArgs(f, "deployment.apps/vminsert"); a[3] != "-storageNode=vmstorage-0.vmstorage.obs-test.svc.cluster.local:8400" || cs[0].(map[string]any)["image"] != "vm:2" {
			t.Fatalf("args %v", a)
		}
	})
	t.Run("endpoint removed by hand after the move", func(t *testing.T) {
		f := setup(t)
		a := liveArgs(f, "deployment.apps/vminsert")
		a[3] = strings.TrimSuffix(a[3].(string), ","+vmEndpoint("vmstorage-b", 1, 8400))
		expectRefused(t, move(t, f, "victoriametrics", storeConfig("victoriametrics"), true), f.calls, "endpoint edited by hand")
	})
	t.Run("annotation re-encoded by hand (same operations, other text)", func(t *testing.T) {
		f := setup(t)
		ann := mapAt(f.objs["deployment.apps/vminsert"], "metadata", "annotations")
		var ops []any
		if err := json.Unmarshal([]byte(ann[inverseAnnotation].(string)), &ops); err != nil {
			t.Fatal(err)
		}
		b, _ := json.MarshalIndent(ops, "", "  ")
		ann[inverseAnnotation] = string(b)
		err := move(t, f, "victoriametrics", storeConfig("victoriametrics"), true)
		expectRefused(t, err, f.calls, "re-encoded inverse")
		if !strings.Contains(err.Error(), "beyond the change obs made") {
			t.Fatalf("refused for another reason: %v", err)
		}
	})
	t.Run("annotation edited to restore another image", func(t *testing.T) {
		f := setup(t)
		ann := mapAt(f.objs["deployment.apps/vminsert"], "metadata", "annotations")
		var ops []op
		if err := json.Unmarshal([]byte(ann[inverseAnnotation].(string)), &ops); err != nil {
			t.Fatal(err)
		}
		ops = append(ops, op{"replace", "/spec/template/spec/containers/0/image", "evil:1"})
		b, _ := json.Marshal(ops)
		ann[inverseAnnotation] = string(b)
		expectRefused(t, move(t, f, "victoriametrics", storeConfig("victoriametrics"), true), f.calls, "tampered inverse")
	})
	t.Run("rollback config derives other endpoints (another cluster_domain)", func(t *testing.T) {
		f := setup(t)
		cfg := storeConfig("victoriametrics")
		cfg.ClusterDomain = "other.local"
		err := move(t, f, "victoriametrics", cfg, true)
		expectRefused(t, err, f.calls, "other derived endpoints")
		if !strings.Contains(err.Error(), "inverse") {
			t.Fatalf("refused for another reason: %v", err)
		}
	})
	t.Run("rollback manifest with other replicas: not the objects obs created", func(t *testing.T) {
		f := setup(t)
		cfg := storeConfig("victoriametrics")
		cfg.ScaledManifest = writeManifest(t, strings.Replace(storageScaled("victoriametrics", false), "  replicas: 2\n", "  replicas: 3\n", 1))
		expectRefused(t, move(t, f, "victoriametrics", cfg, true), f.calls, "other manifest")
	})
	t.Run("Tempo single scaled up by hand after the stop", func(t *testing.T) {
		f := fakeFor(t, "tempo")
		if err := move(t, f, "tempo", config(), false); err != nil {
			t.Fatal(err)
		}
		f.calls = nil
		mapAt(f.objs["statefulset.apps/single"], "spec")["replicas"] = float64(1)
		expectRefused(t, move(t, f, "tempo", config(), true), f.calls, "single changed after stop")
	})
	t.Run("Tempo single at 0 not stopped by obs", func(t *testing.T) {
		f := newFake(t, strings.Replace(clusterLive("tempo"), "  replicas: 1\n", "  replicas: 0\n", 1))
		expectRefused(t, move(t, f, "tempo", config(), true), f.calls, "unknown stopped single")
	})
}

// Correlation round 2/3 ported: the restored single's first pod already has its roles,
// because replicas and roles come back in ONE patch.
func TestRestoreRolesAndReplicasInOnePatch(t *testing.T) {
	f := fakeFor(t, "tempo")
	if err := move(t, f, "tempo", config(), false); err != nil {
		t.Fatal(err)
	}
	f.calls = nil
	if err := f.kube().Execute(context.Background(), Restore, "tempo", config()); err != nil {
		t.Fatal(err)
	}
	p := callsWith(f.calls, "patch statefulset.apps/single")
	if len(p) != 1 || !strings.Contains(p[0], `"path":"/spec/replicas","value":1`) || !strings.Contains(p[0], `labels/obs.diene~1tempo-read","value":"true"`) {
		t.Fatalf("want one patch with replicas and roles: %v", p)
	}
	if l := f.podLabels("single-0"); l["obs.diene/tempo-read"] != "true" {
		t.Fatalf("first pod without roles: %v", l)
	}
}

// ---- create-only ----

func TestCreateOnly(t *testing.T) {
	t.Run("created file: flattened, namespaced, owner label and digest; create never apply", func(t *testing.T) {
		cfg := config()
		cfg.ScaledManifest = writeManifest(t, "apiVersion: v1\nkind: List\nitems:\n"+indentList(scaledManifest("tempo")))
		f := fakeFor(t, "tempo")
		if err := move(t, f, "tempo", cfg, false); err != nil {
			t.Fatal(err)
		}
		if len(f.created) != 1 || strings.Contains(f.created[0], "kind: List") || strings.Count(f.created[0], "namespace: obs-test") != 3 || strings.Count(f.created[0], digestAnnotation) != 3 {
			t.Fatalf("created file:\n%v", f.created)
		}
	})
	for _, tc := range []struct{ name, live string }{
		{"existing unowned ConfigMap", "kind: ConfigMap\nmetadata: {name: new-config}\ndata: {a: live}\n"},
		{"owner label spoofed, no digest", "kind: ConfigMap\nmetadata: {name: new-config, labels: {obs.diene/store-move: tempo}}\ndata: {a: live}\n"},
		{"owned but created from another manifest", "kind: ConfigMap\nmetadata: {name: new-config, labels: {obs.diene/store-move: tempo}, annotations: {obs.diene/store-move-digest: 0000}}\n"},
		{"existing headless Service (single's)", "kind: Service\nmetadata: {name: new-headless}\nspec: {clusterIP: None, selector: {component: single}}\n"},
		{"a pod the new selector would adopt", "kind: Pod\nmetadata: {name: stray}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t, clusterLive("tempo"))
			if strings.HasPrefix(tc.live, "kind: Pod") {
				f.pods["stray"] = map[string]any{"metadata": map[string]any{"name": "stray", "labels": map[string]any{"component": "new"}}}
			} else {
				f2 := newFake(t, tc.live)
				for k, v := range f2.objs {
					f.objs[k] = v
				}
			}
			expectRefused(t, move(t, f, "tempo", config(), false), f.calls, tc.name)
		})
	}
	t.Run("partial create resumes with the absent objects only", func(t *testing.T) {
		f := fakeFor(t, "tempo")
		objs, err := validateScaledManifest(config(), "tempo")
		if err != nil {
			t.Fatal(err)
		}
		file, err := createFile(config().normalized(), "tempo", objs[2:]) // the ConfigMap only
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.run(context.Background(), "--context", "fixture", "--namespace", "obs-test", "create", "-f", file); err != nil {
			t.Fatal(err)
		}
		f.created = nil
		if err := move(t, f, "tempo", config(), false); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(f.created[0], "kind: ConfigMap") || !strings.Contains(f.created[0], "kind: StatefulSet") {
			t.Fatalf("resume created %s", f.created[0])
		}
	})
}

func indentList(body string) string {
	var out []string
	for _, doc := range strings.Split(body, "---\n") {
		lines := strings.Split(strings.TrimSuffix(doc, "\n"), "\n")
		for i, l := range lines {
			if i == 0 {
				out = append(out, "  - "+l)
			} else {
				out = append(out, "    "+l)
			}
		}
	}
	return strings.Join(out, "\n") + "\n"
}

// ---- re-run at every step ----

// A planted failure at each step, then a re-run without it: the re-run starts at the
// step the live state shows, never redoes Prepare, and ends in the same state as an
// uninterrupted move.
func TestRerunAfterFailureAtEveryStep(t *testing.T) {
	clean := fakeFor(t, "tempo")
	if err := move(t, clean, "tempo", config(), false); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, prefix string
		resumeLine   string // printed by the re-run
	}{
		{"create", "create ", ""},
		{"scaled not Ready", "rollout status statefulset.apps/new", ""},
		{"scaled template patch", "patch statefulset.apps/new", ""},
		{"single relabel", "label pods -l component=single", "2. flip role labels to scaled pods\n"},
		{"Alloy roll", "rollout restart deployment.apps/alloy", "2. flip role labels to scaled pods (already live; resuming)"},
		{"stop compaction", "exec ", "2. flip role labels to scaled pods (already live; resuming)"},
		{"stop single", "patch statefulset.apps/single", "2. flip role labels to scaled pods (already live; resuming)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := fakeFor(t, "tempo")
			f.fail = func(cmd string) error {
				if strings.HasPrefix(cmd, tc.prefix) {
					return errors.New("planted")
				}
				return nil
			}
			if err := move(t, f, "tempo", config(), false); err == nil {
				t.Fatal("planted failure ignored")
			}
			f.fail, f.calls = nil, nil
			var out bytes.Buffer
			if err := runner(f, metricsFor("tempo", false), &out).Run(context.Background(), "tempo", config(), false, false); err != nil {
				t.Fatalf("re-run: %v\n%s", err, out.String())
			}
			if tc.resumeLine != "" && !strings.Contains(out.String(), tc.resumeLine) {
				t.Fatalf("re-run plan:\n%s", out.String())
			}
			for res, obj := range clean.snapshot() {
				if !reflect.DeepEqual(f.snapshot()[res], obj) {
					t.Fatalf("%s differs from the uninterrupted move at %s", res, firstDiff(f.snapshot()[res], obj))
				}
			}
			if !reflect.DeepEqual(f.pods, clean.pods) {
				t.Fatalf("pods differ: %v vs %v", f.pods, clean.pods)
			}
		})
	}
	t.Run("completed move: re-run only verifies", func(t *testing.T) {
		clean.calls = nil
		if err := move(t, clean, "tempo", config(), false); err != nil {
			t.Fatal(err)
		}
		if m := mutating(clean.calls); m != "" {
			t.Fatalf("completed move changed again: %s", m)
		}
	})
	t.Run("storage: failure at the second router", func(t *testing.T) {
		f := fakeFor(t, "victoriametrics")
		f.fail = func(cmd string) error {
			if strings.HasPrefix(cmd, "patch deployment.apps/vmselect") {
				return errors.New("planted")
			}
			return nil
		}
		if err := move(t, f, "victoriametrics", storeConfig("victoriametrics"), false); err == nil {
			t.Fatal("planted failure ignored")
		}
		f.fail, f.calls = nil, nil
		if err := move(t, f, "victoriametrics", storeConfig("victoriametrics"), false); err != nil {
			t.Fatal(err)
		}
		if len(callsWith(f.calls, "patch deployment.apps/vminsert")) != 0 || len(callsWith(f.calls, "patch deployment.apps/vmselect")) != 1 || len(callsWith(f.calls, "create ")) != 0 {
			t.Fatalf("re-run calls: %v", f.calls)
		}
	})
	t.Run("roles moved off the single with no scaled roles: refused", func(t *testing.T) {
		f := fakeFor(t, "tempo")
		for _, r := range roles("tempo") {
			f.podLabels("single-0") // ensure exists
			mapAt(f.pods["single-0"], "metadata")["labels"].(map[string]any)[r] = "false"
		}
		expectRefused(t, move(t, f, "tempo", config(), false), f.calls, "empty Services")
	})
}
