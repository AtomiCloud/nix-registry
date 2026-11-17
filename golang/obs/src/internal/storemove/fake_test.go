package storemove

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// fakeKube is a stateful fake kubectl: it stores objects, creates pods from workload
// templates, refuses `create` of an existing object (AlreadyExists), applies `patch
// --type=json` with RFC 6902 semantics including `test` (all or nothing), relabels pods
// and re-creates a workload's pods when its spec changes (a rollout). No test contacts a
// real cluster.
type fakeKube struct {
	t        *testing.T
	objs     map[string]map[string]any // qualified resource -> object
	pods     map[string]map[string]any // pod name -> pod
	owner    map[string]string         // pod name -> workload resource
	rs       map[string]map[string]any // Deployment resource -> its ReplicaSet
	calls    []string
	notReady map[string]bool
	// fail returns a planted error for a command line (before it takes effect).
	fail func(cmd string) error
	// beforePatch runs just before a patch is applied (simulates a concurrent writer).
	beforePatch func(resource string)
	created     []string // the YAML obs passed to create, per call
}

func newFake(t *testing.T, fixtures ...string) *fakeKube {
	f := &fakeKube{t: t, objs: map[string]map[string]any{}, pods: map[string]map[string]any{}, owner: map[string]string{}, rs: map[string]map[string]any{}, notReady: map[string]bool{}}
	for _, body := range fixtures {
		dec := yaml.NewDecoder(strings.NewReader(body))
		for {
			var m map[string]any
			if err := dec.Decode(&m); err == io.EOF {
				break
			} else if err != nil {
				t.Fatal(err)
			}
			if m != nil {
				f.put(m)
			}
		}
	}
	return f
}

func resourceOf(m map[string]any) string {
	kind, _ := m["kind"].(string)
	name, _ := mapAt(m, "metadata")["name"].(string)
	return qualified(canonicalKind(kind) + "/" + name)
}

// put stores an object the way the API server would (JSON form, server fields added).
func (f *fakeKube) put(m map[string]any) {
	m, _ = norm(m).(map[string]any)
	meta, _ := m["metadata"].(map[string]any)
	meta["namespace"], meta["uid"], meta["resourceVersion"] = "obs-test", "uid-"+resourceOf(m), "1"
	if pod := mapAt(m, "spec", "template", "spec"); pod != nil {
		pod["dnsPolicy"], pod["restartPolicy"] = "ClusterFirst", "Always"
	}
	f.objs[resourceOf(m)] = m
	f.reconcile(resourceOf(m))
}

func workloadKind(res string) bool {
	return strings.HasPrefix(res, "statefulset.apps/") || strings.HasPrefix(res, "deployment.apps/")
}

// reconcile re-creates the pods of a workload from its template (a rollout), the way
// the controllers do: StatefulSet pods <name>-<ordinal> (spec.ordinals.start honoured)
// controlled by the StatefulSet; Deployment pods controlled by a ReplicaSet controlled by
// the Deployment, whose revision annotation the controller bumps on every rollout.
func (f *fakeKube) reconcile(res string) {
	if !workloadKind(res) {
		return
	}
	for p, o := range f.owner {
		if o == res {
			delete(f.pods, p)
			delete(f.owner, p)
		}
	}
	m := f.objs[res]
	if m == nil {
		return
	}
	n, _ := intField(mapAt(m, "spec")["replicas"])
	start, _ := ordinalStart(m, res)
	kind, name, _ := strings.Cut(res, "/")
	ownerKind, ownerUID := "StatefulSet", mapAt(m, "metadata")["uid"]
	if kind == "deployment.apps" {
		meta := mapAt(m, "metadata")
		ann, _ := meta["annotations"].(map[string]any)
		if ann == nil {
			ann = map[string]any{}
			meta["annotations"] = ann
		}
		rev, _ := ann["deployment.kubernetes.io/revision"].(string)
		next := 1
		fmt.Sscan(rev, &next)
		if rev != "" {
			next++
		}
		ann["deployment.kubernetes.io/revision"] = fmt.Sprint(next)
		labels, _ := deepCopy(mapAt(m, "spec", "template", "metadata")["labels"]).(map[string]any)
		f.rs[res] = map[string]any{"kind": "ReplicaSet", "metadata": map[string]any{"name": name + "-rs", "uid": "uid-rs-" + name, "labels": labels,
			"ownerReferences": []any{map[string]any{"kind": "Deployment", "name": name, "uid": ownerUID, "controller": true}}}}
		ownerKind, ownerUID = "ReplicaSet", "uid-rs-"+name
	}
	for i := start; i < start+n; i++ {
		labels, _ := deepCopy(mapAt(m, "spec", "template", "metadata")["labels"]).(map[string]any)
		p := fmt.Sprintf("%s-%d", name, i)
		f.pods[p] = map[string]any{"kind": "Pod", "metadata": map[string]any{"name": p, "uid": "uid-pod-" + p, "labels": labels,
			"ownerReferences": []any{map[string]any{"kind": ownerKind, "name": name, "uid": ownerUID, "controller": true}}}}
		f.owner[p] = res
	}
}

func matches(labels map[string]string, sel string) bool {
	for _, pair := range strings.Split(sel, ",") {
		k, v, _ := strings.Cut(pair, "=")
		if got, ok := labels[k]; !ok || got != v { // a missing label never matches, not even ""
			return false
		}
	}
	return true
}

func (f *fakeKube) withStatus(res string) map[string]any {
	m, _ := deepCopy(f.objs[res]).(map[string]any)
	if workloadKind(res) {
		n, _ := intField(mapAt(m, "spec")["replicas"])
		ready := n
		if f.notReady[res] {
			ready = 0
		}
		m["status"] = map[string]any{"replicas": n, "readyReplicas": ready, "updatedReplicas": n}
	}
	return m
}

func (f *fakeKube) kube() Kubernetes { return Kubernetes{Command: f.run} }

func (f *fakeKube) run(_ context.Context, args ...string) ([]byte, error) {
	if len(args) < 4 || args[0] != "--context" || args[2] != "--namespace" || args[3] != "obs-test" {
		f.t.Fatalf("kubectl without explicit context/namespace: %v", args)
	}
	args = args[4:]
	cmd := strings.Join(args, " ")
	f.calls = append(f.calls, cmd)
	if f.fail != nil {
		if err := f.fail(cmd); err != nil {
			return nil, err
		}
	}
	switch {
	case len(args) >= 5 && args[0] == "get" && args[1] == "replicasets.apps" && args[2] == "-l":
		var items []any
		for _, res := range sortedKeys(f.rs) {
			if matches(stringMap(mapAt(f.rs[res], "metadata")["labels"]), args[3]) {
				items = append(items, f.rs[res])
			}
		}
		return json.Marshal(map[string]any{"kind": "List", "items": items})
	case args[0] == "patch" && len(args) == 5 && strings.HasPrefix(args[1], "pod/") && args[2] == "--type=json" && args[3] == "-p":
		name := strings.TrimPrefix(args[1], "pod/")
		live, ok := f.pods[name]
		if !ok {
			return nil, fmt.Errorf("NotFound %s", args[1])
		}
		if f.beforePatch != nil {
			f.beforePatch(args[1])
			live = f.pods[name]
		}
		var ops []op
		if err := json.Unmarshal([]byte(args[4]), &ops); err != nil {
			return nil, err
		}
		next, err := applyPatch(live, ops)
		if err != nil {
			return nil, fmt.Errorf("the server rejected the patch: %w", err)
		}
		f.pods[name] = next
		return nil, nil
	case len(args) >= 5 && args[0] == "get" && args[1] == "pods" && args[2] == "-l":
		var items []any
		for _, name := range sortedKeys(f.pods) {
			p := f.pods[name]
			if matches(stringMap(mapAt(p, "metadata")["labels"]), args[3]) {
				items = append(items, p)
			}
		}
		return json.Marshal(map[string]any{"kind": "List", "items": items})
	case len(args) >= 4 && args[0] == "get":
		if _, ok := f.objs[args[1]]; !ok {
			if strings.Contains(cmd, "--ignore-not-found") {
				return nil, nil
			}
			return nil, fmt.Errorf("NotFound %s", args[1])
		}
		return json.Marshal(f.withStatus(args[1]))
	case args[0] == "create" && len(args) == 3 && args[1] == "-f":
		b, err := os.ReadFile(args[2])
		if err != nil {
			return nil, err
		}
		f.created = append(f.created, string(b))
		dec := yaml.NewDecoder(bytes.NewReader(b))
		var errs []string
		for {
			var m map[string]any
			if err := dec.Decode(&m); err == io.EOF {
				break
			} else if err != nil {
				return nil, err
			}
			if mapAt(m, "metadata")["namespace"] != "obs-test" {
				return nil, fmt.Errorf("create without the namespace")
			}
			if _, ok := f.objs[resourceOf(m)]; ok {
				errs = append(errs, "AlreadyExists "+resourceOf(m))
				continue
			}
			f.put(m)
		}
		if len(errs) > 0 {
			return nil, errors.New(strings.Join(errs, "; "))
		}
		return nil, nil
	case args[0] == "patch" && len(args) == 5 && args[2] == "--type=json" && args[3] == "-p":
		res := args[1]
		live, ok := f.objs[res]
		if !ok {
			return nil, fmt.Errorf("NotFound %s", res)
		}
		if f.beforePatch != nil {
			f.beforePatch(res)
			live = f.objs[res]
		}
		var ops []op
		if err := json.Unmarshal([]byte(args[4]), &ops); err != nil {
			return nil, err
		}
		next, err := applyPatch(live, ops)
		if err != nil {
			return nil, fmt.Errorf("the server rejected the patch: %w", err)
		}
		dropEmptyAnnotations(next)
		specChanged := !reflect.DeepEqual(next["spec"], live["spec"])
		f.objs[res] = next
		if specChanged {
			f.reconcile(res)
		}
		return nil, nil
	case args[0] == "label" && args[1] == "pods" && args[2] == "-l" && args[4] == "--overwrite":
		for _, name := range sortedKeys(f.pods) {
			p := f.pods[name]
			labels, _ := mapAt(p, "metadata")["labels"].(map[string]any)
			if !matches(stringMap(labels), args[3]) {
				continue
			}
			for _, kv := range args[5:] {
				k, v, _ := strings.Cut(kv, "=")
				labels[k] = v
			}
		}
		return nil, nil
	case args[0] == "rollout" && args[1] == "status":
		if _, ok := f.objs[args[2]]; !ok {
			return nil, fmt.Errorf("NotFound %s", args[2])
		}
		if f.notReady[args[2]] {
			return nil, fmt.Errorf("planted: %s not Ready", args[2])
		}
		return nil, nil
	case args[0] == "rollout" && args[1] == "restart":
		f.reconcile(args[2])
		return nil, nil
	case args[0] == "exec":
		return nil, nil
	}
	f.t.Fatalf("unexpected kubectl call: %s", cmd)
	return nil, nil
}

// mutating returns the first call that changes the cluster (reads are allowed).
func mutating(calls []string) string {
	for _, c := range calls {
		for _, verb := range []string{"create ", "patch ", "scale ", "label ", "rollout restart ", "exec ", "apply ", "delete "} {
			if strings.HasPrefix(c, verb) {
				return c
			}
		}
	}
	return ""
}

func expectRefused(t *testing.T, err error, calls []string, what string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s accepted; calls=%v", what, calls)
	}
	if m := mutating(calls); m != "" {
		t.Fatalf("%s refused only after a change: %q (%v)", what, m, err)
	}
	t.Log(err)
}

// ---- fixtures ----

func roles(store string) []string {
	if store == "pyroscope" {
		return []string{"obs.diene/pyroscope-query", "obs.diene/pyroscope-write"}
	}
	return []string{"obs.diene/tempo-read", "obs.diene/tempo-write"}
}

// singleLive is the running single-instance store: every role label true, claims
// retained, a toleration with tolerationSeconds and a CPU quantity (GPT round 5 items 1, 2).
func singleLive(store string) string {
	r := roles(store)
	return `apiVersion: apps/v1
kind: StatefulSet
metadata: {name: single, labels: {app: ` + store + `}}
spec:
  replicas: 1
  serviceName: single-headless
  selector: {matchLabels: {component: single}}
  persistentVolumeClaimRetentionPolicy: {whenDeleted: Retain, whenScaled: Retain}
  template:
    metadata: {labels: {component: single, ` + r[0] + `: "true", ` + r[1] + `: "true"}}
    spec:
      tolerations: [{key: dedicated, operator: Exists, effect: NoExecute, tolerationSeconds: 60}]
      containers:
      - name: store
        image: ` + store + `:1
        args: ["-target=all", "-config.file=/conf/store.yaml", "-metastore.raft.server-id=single-0.single-headless.obs-test.svc.cluster.local:9099"]
        resources: {requests: {cpu: 500m, memory: 1Gi}}
        volumeMounts: [{name: data, mountPath: /data}]
  volumeClaimTemplates:
  - metadata: {name: data}
    spec: {accessModes: [ReadWriteOnce], resources: {requests: {storage: 1Gi}}}
`
}

func clusterLive(store string) string {
	r := roles(store)
	write, read := "tempo-distributor", "tempo-query"
	if store == "pyroscope" {
		write, read = "pyroscope-write", "pyroscope-query"
	}
	return singleLive(store) + `---
kind: Service
metadata: {name: ` + write + `}
spec: {selector: {` + r[1] + `: "true"}, ports: [{name: write, port: 4318}]}
---
kind: Service
metadata: {name: ` + read + `}
spec: {selector: {` + r[0] + `: "true"}, ports: [{name: read, port: 3200}]}
---
apiVersion: apps/v1
kind: Deployment
metadata: {name: alloy}
spec:
  replicas: 2
  selector: {matchLabels: {app: alloy}}
  template:
    metadata: {labels: {app: alloy}}
    spec: {containers: [{name: alloy, image: "alloy:1"}]}
`
}

func scaledManifest(store string) string {
	pnra := ""
	if store == "tempo" {
		pnra = ", publishNotReadyAddresses: true"
	}
	return `apiVersion: apps/v1
kind: StatefulSet
metadata: {name: new}
spec:
  replicas: 2
  serviceName: new-headless
  selector: {matchLabels: {component: new}}
  template:
    metadata: {labels: {component: new}}
    spec:
      containers: [{name: store, image: "` + store + `:1"}]
  volumeClaimTemplates:
  - metadata: {name: data}
    spec: {accessModes: [ReadWriteOnce], resources: {requests: {storage: 1Gi}}}
---
kind: Service
metadata: {name: new-headless}
spec: {clusterIP: None` + pnra + `, selector: {component: new}}
---
kind: ConfigMap
metadata: {name: new-config}
data: {store.yaml: "target: scalable"}
`
}

func vmLive(store string) string {
	if store == "victorialogs" {
		return `apiVersion: apps/v1
kind: Deployment
metadata: {name: vlinsert}
spec:
  replicas: 2
  selector: {matchLabels: {app: vlinsert}}
  template:
    metadata: {labels: {app: vlinsert}}
    spec: {containers: [{name: vlinsert, image: "vl:1", args: ["-httpListenAddr=:9481", "-storageNode=vlstorage-0.vlstorage.obs-test.svc.cluster.local:9491"]}]}
---
apiVersion: apps/v1
kind: Deployment
metadata: {name: vlselect}
spec:
  replicas: 2
  selector: {matchLabels: {app: vlselect}}
  template:
    metadata: {labels: {app: vlselect}}
    spec: {containers: [{name: vlselect, image: "vl:1", args: ["-httpListenAddr=:9471", "-storageNode", "vlstorage-0.vlstorage.obs-test.svc.cluster.local:9491"]}]}
`
	}
	return `apiVersion: apps/v1
kind: Deployment
metadata: {name: vminsert}
spec:
  replicas: 2
  selector: {matchLabels: {app: vminsert}}
  template:
    metadata: {labels: {app: vminsert}}
    spec: {containers: [{name: vminsert, image: "vm:1", args: ["-httpListenAddr=:8480", "-memory.allowedPercent", "60", "-storageNode=vmstorage-0.vmstorage.obs-test.svc.cluster.local:8400"]}]}
---
apiVersion: apps/v1
kind: Deployment
metadata: {name: vmselect}
spec:
  replicas: 2
  selector: {matchLabels: {app: vmselect}}
  template:
    metadata: {labels: {app: vmselect}}
    spec: {containers: [{name: vmselect, image: "vm:1", args: ["-storageNode", "vmstorage-0.vmstorage.obs-test.svc.cluster.local:8401", "-search.maxConcurrentRequests=4"]}]}
---
apiVersion: apps/v1
kind: StatefulSet
metadata: {name: vmstorage}
spec:
  replicas: 1
  serviceName: vmstorage
  selector: {matchLabels: {app: vmstorage}}
  template:
    metadata: {labels: {app: vmstorage}}
    spec: {containers: [{name: vmstorage, image: "vm:1", args: ["-retentionPeriod=30d", "-storageDataPath=/storage"], ports: [{containerPort: 8400}, {containerPort: 8401}]}]}
`
}

func storageScaled(store string, dedup bool) string {
	name, ports, args := "vmstorage-b", "[{containerPort: 8482}, {containerPort: 8400}, {containerPort: 8401}]", `["-retentionPeriod=30d", "-storageDataPath=/storage"]`
	if store == "victorialogs" {
		name, ports, args = "vlstorage-b", "[{containerPort: 9491}]", `["-storageDataPath=/storage"]`
	}
	if dedup {
		args = `["-retentionPeriod=30d", "-dedup.minScrapeInterval=1ms", "-storageDataPath=/storage"]`
	}
	return `apiVersion: apps/v1
kind: StatefulSet
metadata: {name: ` + name + `}
spec:
  replicas: 2
  serviceName: ` + name + `
  selector: {matchLabels: {app: ` + name + `}}
  template:
    metadata: {labels: {app: ` + name + `}}
    spec: {containers: [{name: storage, image: "vm:1", args: ` + args + `, ports: ` + ports + `}]}
  volumeClaimTemplates:
  - metadata: {name: data}
    spec: {accessModes: [ReadWriteOnce], resources: {requests: {storage: 1Gi}}}
---
kind: Service
metadata: {name: ` + name + `}
spec: {clusterIP: None, selector: {app: ` + name + `}}
`
}

func writeManifest(t *testing.T, body string) string {
	t.Helper()
	f := filepath.Join(t.TempDir(), "manifest.yaml")
	if err := os.WriteFile(f, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return f
}

// fixtureDir holds the default scaled manifests used by config()/storeConfig().
var fixtureDir string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "storemove-fixtures-")
	if err != nil {
		panic(err)
	}
	fixtureDir = dir
	for name, body := range map[string]string{
		"tempo.yaml": scaledManifest("tempo"), "pyroscope.yaml": scaledManifest("pyroscope"),
		"victoriametrics.yaml": storageScaled("victoriametrics", false), "victorialogs.yaml": storageScaled("victorialogs", false),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			panic(err)
		}
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func storeConfig(store string) Config {
	c := Config{Context: "fixture", Namespace: "obs-test", ScaledManifest: filepath.Join(fixtureDir, store+".yaml"), MetricsURL: "http://fixture/metrics", ReadURL: "http://fixture/range", Query: "sum(count_over_time())"}
	switch store {
	case "victoriametrics":
		c.Scaled = []string{"statefulset/vmstorage-b"}
		c.Routers = []Router{{"deployment/vminsert", "insert", 8400}, {"deployment/vmselect", "select", 8401}}
	case "victorialogs":
		c.Scaled = []string{"statefulset/vlstorage-b"}
		c.Routers = []Router{{"deployment/vlinsert", "insert", 9491}, {"deployment/vlselect", "select", 9491}}
	default:
		c.Scaled = []string{"statefulset/new"}
		c.Single, c.Alloy = "statefulset/single", "deployment/alloy"
		c.RoleLabels = roles(store)
		c.RoleWorkloads = map[string][]string{"statefulset/new": roles(store)}
		c.SingleMetricsURL = "http://single/metrics"
		if store == "tempo" {
			c.StopCompaction, c.ResumeCompaction = []string{"control", "stop-compaction"}, []string{"control", "resume-compaction"}
		} else {
			c.ProfileType = "process_cpu:cpu:nanoseconds:cpu:nanoseconds"
		}
	}
	return c
}
func config() Config { return storeConfig("tempo") }

func fakeFor(t *testing.T, store string) *fakeKube {
	if storage(store) {
		return newFake(t, vmLive(store))
	}
	return newFake(t, clusterLive(store))
}

// snapshot copies the live state for before/after comparisons, without the revision
// number the Deployment controller (not obs) writes on every rollout.
func (f *fakeKube) snapshot() map[string]map[string]any {
	out := map[string]map[string]any{}
	for k, v := range f.objs {
		c := withoutBookkeeping(v)
		if meta, _ := c["metadata"].(map[string]any); meta != nil {
			if a, _ := meta["annotations"].(map[string]any); a != nil {
				delete(a, "deployment.kubernetes.io/revision")
				if len(a) == 0 {
					delete(meta, "annotations")
				}
			}
		}
		out[k] = c
	}
	return out
}

func (f *fakeKube) podLabels(name string) map[string]string {
	return stringMap(mapAt(f.pods[name], "metadata")["labels"])
}
