package storemove

// GPT (emmalyn) round-6 review at be665f6, ported verbatim except where noted: each test
// passes only when the finding is fixed (they were red against be665f6).

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestReviewer6InverseReplicaTamper(t *testing.T) {
	for _, tamper := range []bool{false, true} {
		t.Run(fmt.Sprint(tamper), func(t *testing.T) {
			f := fakeFor(t, "tempo")
			cfg := config()
			if err := move(t, f, "tempo", cfg, false); err != nil {
				t.Fatal(err)
			}
			if tamper {
				ann := mapAt(f.objs["statefulset.apps/single"], "metadata", "annotations")
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
			err := move(t, f, "tempo", cfg, true)
			if tamper {
				if err == nil {
					t.Fatalf("tampered original replica count accepted; restored replicas=%v", mapAt(f.objs["statefulset.apps/single"], "spec")["replicas"])
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestReviewer6OrdinalClaim(t *testing.T) {
	cfg := config()
	cfg.ScaledManifest = writeManifest(t, strings.Replace(scaledManifest("tempo"), "  replicas: 2\n", "  replicas: 2\n  ordinals: {start: 20}\n", 1))
	f := newFake(t, clusterLive("tempo"), "kind: PersistentVolumeClaim\nmetadata: {name: data-new-20}\n")
	err := move(t, f, "tempo", cfg, false)
	if err == nil {
		t.Fatalf("new StatefulSet with ordinals.start=20 accepted despite preexisting data-new-20; actual claim never checked")
	}
}

func TestReviewer6OrdinalEndpoint(t *testing.T) {
	cfg := storeConfig("victoriametrics").normalized()
	cfg.ScaledManifest = writeManifest(t, strings.Replace(storageScaled("victoriametrics", false), "  replicas: 2\n", "  replicas: 2\n  ordinals: {start: 20}\n", 1))
	objs, err := validateScaledManifest(cfg, "victoriametrics")
	if err != nil {
		t.Logf("safe refusal: %v", err)
		return
	}
	nodes, err := storageNodes(cfg, objs)
	if err != nil {
		t.Fatal(err)
	}
	got := endpoints(cfg, nodes, 8400)
	want := []string{vmEndpoint("vmstorage-b", 20, 8400), vmEndpoint("vmstorage-b", 21, 8400)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("derived endpoints name nonexistent pods: got %v want %v", got, want)
	}
}

func TestReviewer6LatePod(t *testing.T) {
	f := fakeFor(t, "tempo")
	injected := false
	f.fail = func(cmd string) error {
		if !injected && strings.HasPrefix(cmd, "create ") {
			injected = true
			f.pods["late-existing-pod"] = map[string]any{"metadata": map[string]any{"name": "late-existing-pod", "labels": map[string]any{"component": "new"}}}
		}
		return nil
	}
	err := move(t, f, "tempo", config(), false)
	if err == nil {
		t.Fatalf("move accepted pod arriving between last absent check and create; unrelated pod labels now %v", f.podLabels("late-existing-pod"))
	}
	// Ported addition: the unrelated pod is never relabelled.
	for _, r := range roles("tempo") {
		if v, ok := f.podLabels("late-existing-pod")[r]; ok {
			t.Fatalf("unrelated pod got %s=%s", r, v)
		}
	}
}

func TestReviewer6CreateRaceAndResume(t *testing.T) {
	f := fakeFor(t, "tempo")
	injected := false
	f.fail = func(cmd string) error {
		if !injected && strings.HasPrefix(cmd, "create ") {
			injected = true
			x := newFake(t, "kind: ConfigMap\nmetadata: {name: new-config}\ndata: {owner: concurrent-writer}\n")
			f.objs["configmap/new-config"] = x.objs["configmap/new-config"]
		}
		return nil
	}
	err := move(t, f, "tempo", config(), false)
	if err == nil || !strings.Contains(err.Error(), "AlreadyExists") {
		t.Fatalf("expected safe create conflict, got %v", err)
	}
	if got := mapAt(f.objs["configmap/new-config"], "data")["owner"]; got != "concurrent-writer" {
		t.Fatalf("racing object overwritten: %v", got)
	}
	f.fail = nil
	f.calls = nil
	err = move(t, f, "tempo", config(), false)
	expectRefused(t, err, f.calls, "unowned object after partial create")
	delete(f.objs, "configmap/new-config")
	f.calls = nil
	f.created = nil
	if err := move(t, f, "tempo", config(), false); err != nil {
		t.Fatal(err)
	}
	if len(f.created) != 1 || strings.Contains(f.created[0], "kind: StatefulSet") || !strings.Contains(f.created[0], "kind: ConfigMap") {
		t.Fatalf("partial create rerun not restricted to absent object: %v", f.created)
	}
}

func TestReviewer6ArrayIndexDrift(t *testing.T) {
	f := fakeFor(t, "victoriametrics")
	f.beforePatch = func(res string) {
		if res != "deployment.apps/vminsert" {
			return
		}
		f.beforePatch = nil
		cs := mapAt(f.objs[res], "spec", "template", "spec")["containers"].([]any)
		cm := cs[0].(map[string]any)
		a := cm["args"].([]any)
		// Duplicate the old flag at its original index, pushing the real one right.
		cm["args"] = append(a, a[3])
	}
	err := move(t, f, "victoriametrics", storeConfig("victoriametrics"), false)
	if err == nil {
		t.Fatalf("array drift accepted, creating two storageNode flags despite exact-one precondition: %v", liveArgs(f, "deployment.apps/vminsert"))
	}
}

func TestReviewer6ReplicationRace(t *testing.T) {
	cfg := storeConfig("victoriametrics")
	cfg.Copies = 2
	cfg.Storage = []string{"statefulset/vmstorage"}
	cfg.ScaledManifest = writeManifest(t, storageScaled("victoriametrics", true))
	f := fakeFor(t, "victoriametrics")
	f.beforePatch = func(res string) {
		if res != "deployment.apps/vminsert" {
			return
		}
		f.beforePatch = nil
		cs := mapAt(f.objs[res], "spec", "template", "spec")["containers"].([]any)
		cm := cs[0].(map[string]any)
		cm["args"] = append(cm["args"].([]any), "-replicationFactor=1")
	}
	err := move(t, f, "victoriametrics", cfg, false)
	if err == nil {
		fs := flag.NewFlagSet("vminsert", flag.ContinueOnError)
		rf := fs.Int("replicationFactor", 1, "")
		fs.String("storageNode", "", "")
		fs.String("httpListenAddr", "", "")
		fs.Int("memory.allowedPercent", 0, "")
		var a []string
		for _, v := range liveArgs(f, "deployment.apps/vminsert") {
			a = append(a, v.(string))
		}
		if e := fs.Parse(a); e != nil {
			t.Fatal(e)
		}
		t.Fatalf("copies 2 move succeeded, concurrent appended flag wins: effective replicationFactor=%d; args=%v", *rf, a)
	}
}

func TestReviewer6LateRetentionDelete(t *testing.T) {
	f := newFake(t, strings.Replace(clusterLive("tempo"), "  persistentVolumeClaimRetentionPolicy: {whenDeleted: Retain, whenScaled: Retain}\n", "", 1))
	f.beforePatch = func(res string) {
		if res == "statefulset.apps/single" {
			mapAt(f.objs[res], "spec")["persistentVolumeClaimRetentionPolicy"] = map[string]any{"whenScaled": "Delete", "whenDeleted": "Retain"}
		}
	}
	err := move(t, f, "tempo", config(), false)
	if err == nil {
		t.Fatalf("scaled single to %v with concurrent live whenScaled Delete", mapAt(f.objs["statefulset.apps/single"], "spec")["replicas"])
	}
}

func TestReviewer6StableSelectorMustMatchScaled(t *testing.T) {
	f := newFake(t, strings.ReplaceAll(clusterLive("tempo"), "spec: {selector: {obs.diene/tempo-", "spec: {selector: {component: single, obs.diene/tempo-"))
	err := move(t, f, "tempo", config(), false)
	expectRefused(t, err, f.calls, "stable Service selector with a single-only term") // ported addition
	if err == nil {
		for _, res := range []string{"service/tempo-query", "service/tempo-distributor"} {
			sel := selectorString(stringMap(mapAt(f.objs[res], "spec")["selector"]))
			ps, e := f.kube().pods(context.Background(), config(), stringMap(mapAt(f.objs[res], "spec")["selector"]))
			if e != nil {
				t.Fatal(e)
			}
			if len(ps) == 0 {
				t.Errorf("successful move leaves stable %s with selector %s and zero selected pods", res, sel)
			}
		}
	}
}

func TestReviewer6PointerEscapesAndOrdinaryDrift(t *testing.T) {
	doc := map[string]any{"metadata": map[string]any{"labels": map[string]any{"a/b~c": "old"}}}
	p := pointer("metadata", "labels", "a/b~c")
	if p != "/metadata/labels/a~1b~0c" {
		t.Fatalf("pointer %s", p)
	}
	ops := []op{{"test", p, "old"}, {"replace", p, "new"}}
	inv, err := invert(doc, ops)
	if err != nil {
		t.Fatal(err)
	}
	next, err := applyPatch(doc, ops)
	if err != nil {
		t.Fatal(err)
	}
	orig, err := applyPatch(next, inv)
	if err != nil || !reflect.DeepEqual(orig, doc) {
		t.Fatalf("escaped path failed roundtrip: %v %v", orig, err)
	}
	f := fakeFor(t, "victoriametrics")
	f.beforePatch = func(res string) {
		if res != "deployment.apps/vminsert" {
			return
		}
		f.beforePatch = nil
		cs := mapAt(f.objs[res], "spec", "template", "spec")["containers"].([]any)
		cm := cs[0].(map[string]any)
		a := cm["args"].([]any)
		cm["args"] = append([]any{"-http.disableResponseCompression"}, a...)
	}
	err = f.kube().Execute(context.Background(), StorageRoute, "victoriametrics", storeConfig("victoriametrics"))
	if err == nil || !strings.Contains(err.Error(), "test ") {
		t.Fatalf("ordinary index drift not refused: %v", err)
	}
}

func TestReviewer6PriorReprosOnRedesign(t *testing.T) {
	t.Run("old mutation manifests no longer accepted", func(t *testing.T) {
		for _, field := range []string{"single_manifest", "metastore_manifest", "routing_manifest", "original_routing_manifest"} {
			dec := yaml.NewDecoder(strings.NewReader(field + ": bypass.yaml\n"))
			dec.KnownFields(true)
			var cfg Config
			if err := dec.Decode(&cfg); err == nil {
				t.Errorf("deleted mutation input %s accepted", field)
			}
		}
	})
	t.Run("Opus exact claim name collision", func(t *testing.T) {
		cfg := config()
		cfg.Single = "statefulset/obs-tempo"
		cfg.Scaled = []string{"statefulset/tempo"}
		cfg.RoleWorkloads = map[string][]string{"statefulset/tempo": cfg.RoleLabels}
		cfg.ScaledManifest = writeManifest(t, strings.ReplaceAll(strings.ReplaceAll(scaledManifest("tempo"), "new", "tempo"), "metadata: {name: data}", "metadata: {name: storage-obs}"))
		live := strings.Replace(clusterLive("tempo"), "metadata: {name: single,", "metadata: {name: obs-tempo,", 1)
		f := newFake(t, live, "kind: PersistentVolumeClaim\nmetadata: {name: storage-obs-tempo-0}\n")
		expectRefused(t, move(t, f, "tempo", cfg, false), f.calls, "storage-obs-tempo-0 adoption")
	})
	t.Run("GPT live quantity and atomic toleration unchanged", func(t *testing.T) {
		f := fakeFor(t, "pyroscope")
		before := f.snapshot()["statefulset.apps/single"]
		if err := move(t, f, "pyroscope", storeConfig("pyroscope"), false); err != nil {
			t.Fatal(err)
		}
		if err := move(t, f, "pyroscope", storeConfig("pyroscope"), true); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(before, f.snapshot()["statefulset.apps/single"]) {
			t.Fatal("existing fields lost")
		}
	})
}
