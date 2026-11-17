package storemove

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// Ported reviewer repros (rounds 1-4) that still apply to the scaled manifest, plus the
// round-5 attacks on the redesign. Every refusal must happen before the first change.

func scaledRefused(t *testing.T, store, body, what string) {
	t.Helper()
	cfg := storeConfig(store)
	cfg.ScaledManifest = writeManifest(t, body)
	f := fakeFor(t, store)
	expectRefused(t, move(t, f, store, cfg, false), f.calls, what)
}
func scaledAccepted(t *testing.T, store, body string) {
	t.Helper()
	cfg := storeConfig(store)
	cfg.ScaledManifest = writeManifest(t, body)
	f := fakeFor(t, store)
	if err := move(t, f, store, cfg, false); err != nil {
		t.Fatalf("baseline refused: %v", err)
	}
}

// Rounds 1-4: supporting objects are the store's own (kind AND name), new, and harmless.
func TestPortedScaledSupportingObjects(t *testing.T) {
	base := scaledManifest("tempo")
	for _, tc := range []struct {
		name, extra string
		ok          bool
	}{
		{"baseline: name derived from the single (gossip ring)", "kind: Service\nmetadata: {name: single-gossip-ring}\nspec: {clusterIP: None, publishNotReadyAddresses: true}\n", true},
		{"app ConfigMap name (checkout-env)", "kind: ConfigMap\nmetadata: {name: checkout-env}\ndata: {DATABASE_URL: wrong}\n", false},
		{"app Service name (checkout)", "kind: Service\nmetadata: {name: checkout}\nspec: {clusterIP: None, publishNotReadyAddresses: true, selector: {app: unrelated}}\n", false},
		{"app RoleBinding to cluster-admin", "kind: RoleBinding\napiVersion: rbac.authorization.k8s.io/v1\nmetadata: {name: checkout-admin}\nroleRef: {apiGroup: rbac.authorization.k8s.io, kind: ClusterRole, name: cluster-admin}\nsubjects: [{kind: ServiceAccount, name: checkout}]\n", false},
		{"non-headless Service", "kind: Service\nmetadata: {name: new-svc}\nspec: {publishNotReadyAddresses: true}\n", false},
		{"Tempo headless Service without publishNotReadyAddresses", "kind: Service\nmetadata: {name: new-headless2}\nspec: {clusterIP: None}\n", false},
		{"headless Service of type LoadBalancer", "kind: Service\nmetadata: {name: new-lb}\nspec: {clusterIP: None, publishNotReadyAddresses: true, type: LoadBalancer}\n", false},
		{"headless Service with externalIPs", "kind: Service\nmetadata: {name: new-ext}\nspec: {clusterIP: None, publishNotReadyAddresses: true, externalIPs: [1.2.3.4]}\n", false},
		{"stable Service tempo-query", "kind: Service\nmetadata: {name: tempo-query}\nspec: {clusterIP: None, publishNotReadyAddresses: true}\n", false},
		{"Alloy ConfigMap", "kind: ConfigMap\nmetadata: {name: alloy}\ndata: {config.alloy: x}\n", false},
		{"Secret", "kind: Secret\nmetadata: {name: new-secret}\n", false},
		{"PVC", "kind: PersistentVolumeClaim\nmetadata: {name: new-data}\n", false},
		{"Endpoints", "kind: Endpoints\nmetadata: {name: new-ep}\n", false},
		{"EndpointSlice", "apiVersion: discovery.k8s.io/v1\nkind: EndpointSlice\nmetadata: {name: new-eps, labels: {kubernetes.io/service-name: tempo-query}}\n", false},
		{"DaemonSet", "apiVersion: apps/v1\nkind: DaemonSet\nmetadata: {name: new-ds}\n", false},
		{"ClusterRole", "apiVersion: rbac.authorization.k8s.io/v1\nkind: ClusterRole\nmetadata: {name: new-cr}\n", false},
		{"RBAC outside Pyroscope", "kind: ServiceAccount\nmetadata: {name: new-sa}\n", false},
		{"unlisted workload", "apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: unlisted}\nspec: {replicas: 1, selector: {matchLabels: {component: unlisted}}, template: {metadata: {labels: {component: unlisted}}}}\n", false},
		{"the single itself", "apiVersion: apps/v1\nkind: StatefulSet\nmetadata: {name: single}\nspec: {replicas: 1}\n", false},
		{"Alloy itself", "apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: alloy}\nspec: {replicas: 1}\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := base + "---\n" + tc.extra
			if tc.ok {
				scaledAccepted(t, "tempo", body)
				return
			}
			scaledRefused(t, "tempo", body, tc.name)
		})
	}
}

func TestPortedScaledWorkloadPlants(t *testing.T) {
	base := scaledManifest("tempo")
	for _, tc := range []struct{ name, from, to string }{
		{"replicas 0", "  replicas: 2\n", "  replicas: 0\n"},
		{"replicas absent", "  replicas: 2\n", ""},
		{"role label true in template (routes before the role step)", "labels: {component: new}}", "labels: {component: new, obs.diene/tempo-read: \"true\"}}"},
		{"role label in the selector", "matchLabels: {component: new}}", "matchLabels: {component: new, obs.diene/tempo-read: \"false\"}}"},
		{"selector expressions", "matchLabels: {component: new}}", "matchLabels: {component: new}, matchExpressions: [{key: a, operator: Exists}]}"},
		{"selector overlapping the single's pods", "matchLabels: {component: new}}", "matchLabels: {app: x}}"},
		{"existing claim mounted", "      containers: [{name: store, image: \"tempo:1\"}]\n", "      containers: [{name: store, image: \"tempo:1\"}]\n      volumes: [{name: d, persistentVolumeClaim: {claimName: data-single-0}}]\n"},
		{"host path mounted", "      containers: [{name: store, image: \"tempo:1\"}]\n", "      containers: [{name: store, image: \"tempo:1\"}]\n      volumes: [{name: d, hostPath: {path: /var/lib/single}}]\n"},
		{"hostNetwork", "      containers: [{name: store, image: \"tempo:1\"}]\n", "      hostNetwork: true\n      containers: [{name: store, image: \"tempo:1\"}]\n"},
		{"hostPID", "      containers: [{name: store, image: \"tempo:1\"}]\n", "      hostPID: true\n      containers: [{name: store, image: \"tempo:1\"}]\n"},
		{"runs as an existing ServiceAccount (alloy)", "      containers: [{name: store, image: \"tempo:1\"}]\n", "      serviceAccountName: alloy\n      containers: [{name: store, image: \"tempo:1\"}]\n"},
		{"deprecated serviceAccount field", "      containers: [{name: store, image: \"tempo:1\"}]\n", "      serviceAccount: argocd-server\n      containers: [{name: store, image: \"tempo:1\"}]\n"},
		{"owner label of another store", "metadata: {name: new}\n", "metadata: {name: new, labels: {obs.diene/store-move: pyroscope}}\n"},
		{"digest annotation written by hand", "metadata: {name: new}\n", "metadata: {name: new, annotations: {obs.diene/store-move-digest: abc}}\n"},
		{"inverse annotation written by hand", "metadata: {name: new}\n", "metadata: {name: new, annotations: {obs.diene/store-move-inverse: \"[]\"}}\n"},
		{"listed workload missing", "metadata: {name: new}\n", "metadata: {name: new2}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !strings.Contains(base, tc.from) {
				t.Fatalf("fixture lacks %q", tc.from)
			}
			scaledRefused(t, "tempo", strings.Replace(base, tc.from, tc.to, 1), tc.name)
		})
	}
	t.Run("two new workloads with overlapping selectors", func(t *testing.T) {
		cfg := config()
		cfg.Scaled = []string{"statefulset/new", "deployment/new-query"}
		cfg.RoleWorkloads = map[string][]string{"statefulset/new": {"obs.diene/tempo-write"}, "deployment/new-query": {"obs.diene/tempo-read"}}
		cfg.ScaledManifest = writeManifest(t, base+"---\napiVersion: apps/v1\nkind: Deployment\nmetadata: {name: new-query}\nspec: {replicas: 1, selector: {matchLabels: {component: new}}, template: {metadata: {labels: {component: new}}}}\n")
		f := fakeFor(t, "tempo")
		expectRefused(t, move(t, f, "tempo", cfg, false), f.calls, "overlapping new selectors")
	})
	t.Run("own ServiceAccount (Pyroscope) is allowed", func(t *testing.T) {
		scaledAccepted(t, "pyroscope", strings.Replace(scaledManifest("pyroscope"), "      containers: [{name: store, image: \"pyroscope:1\"}]\n", "      serviceAccountName: new-metastore\n      containers: [{name: store, image: \"pyroscope:1\"}]\n", 1)+"---\n"+pyroRBAC)
	})
}

const pyroRBAC = `kind: ServiceAccount
metadata: {name: new-metastore}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata: {name: new-discovery}
rules:
- apiGroups: [""]
  resources: [pods, endpoints]
  verbs: [get, list, watch]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata: {name: new-discovery}
roleRef: {apiGroup: rbac.authorization.k8s.io, kind: Role, name: new-discovery}
subjects: [{kind: ServiceAccount, name: new-metastore}]
`

// Round 4: Pyroscope kubernetes:/// discovery RBAC, nothing more.
func TestPortedPyroscopeRBAC(t *testing.T) {
	for _, tc := range []struct {
		name, from, to string
		ok             bool
	}{
		{"baseline", "", "", true},
		{"Role reads secrets", "resources: [pods, endpoints]", "resources: [pods, endpoints, secrets]", false},
		{"Role may create pods", "verbs: [get, list, watch]", "verbs: [get, list, watch, create]", false},
		{"Role wildcard group", `apiGroups: [""]`, `apiGroups: ["*"]`, false},
		{"Role adds nonResourceURLs", "  verbs: [get, list, watch]\n", "  verbs: [get, list, watch]\n  nonResourceURLs: [/metrics]\n", false},
		{"RoleBinding to cluster-admin", "roleRef: {apiGroup: rbac.authorization.k8s.io, kind: Role, name: new-discovery}", "roleRef: {apiGroup: rbac.authorization.k8s.io, kind: ClusterRole, name: cluster-admin}", false},
		{"RoleBinding to a Role not in the manifest", "kind: Role, name: new-discovery}", "kind: Role, name: new-admin}", false},
		{"RoleBinding grants an app ServiceAccount", "subjects: [{kind: ServiceAccount, name: new-metastore}]", "subjects: [{kind: ServiceAccount, name: checkout}]", false},
		{"RoleBinding subject in another namespace", "subjects: [{kind: ServiceAccount, name: new-metastore}]", "subjects: [{kind: ServiceAccount, name: new-metastore, namespace: apps}]", false},
		{"RoleBinding grants a user", "subjects: [{kind: ServiceAccount, name: new-metastore}]", "subjects: [{kind: User, name: new-metastore}]", false},
		{"existing ServiceAccount name reused (create-only)", "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := pyroRBAC
			if tc.from != "" {
				if !strings.Contains(body, tc.from) {
					t.Fatalf("fixture lacks %q", tc.from)
				}
				body = strings.Replace(body, tc.from, tc.to, 1)
			}
			body = scaledManifest("pyroscope") + "---\n" + body
			if tc.ok {
				scaledAccepted(t, "pyroscope", body)
				return
			}
			cfg := storeConfig("pyroscope")
			cfg.ScaledManifest = writeManifest(t, body)
			f := fakeFor(t, "pyroscope")
			if strings.HasPrefix(tc.name, "existing") {
				f = newFake(t, clusterLive("pyroscope"), "kind: ServiceAccount\nmetadata: {name: new-metastore}\n")
			}
			expectRefused(t, move(t, f, "pyroscope", cfg, false), f.calls, tc.name)
		})
	}
}

// Round 4: manifest shapes kubectl reads differently. obs creates the flattened objects
// itself, so a List is fine, but anything ambiguous is refused.
func TestPortedManifestShapes(t *testing.T) {
	good := scaledManifest("tempo")
	for _, tc := range []struct {
		name, body string
		ok         bool
	}{
		{"baseline inside a List", "apiVersion: v1\nkind: List\nitems:\n" + indentList(good), true},
		{"items smuggled on a ConfigMap", good + "---\nkind: ConfigMap\nmetadata: {name: new-x}\nitems:\n  - kind: Service\n    metadata: {name: tempo-query}\n", false},
		{"typed list kind", "kind: ServiceList\nitems:\n" + indentList(good), false},
		{"List with another apiVersion", "apiVersion: v2\nkind: List\nitems:\n" + indentList(good), false},
		{"lower-case kind", strings.Replace(good, "kind: ConfigMap", "kind: configmap", 1), false},
		{"alias kind", strings.Replace(good, "kind: StatefulSet", "kind: sts", 1), false},
		{"CRD reusing a Kind name", good + "---\napiVersion: example.com/v1\nkind: ConfigMap\nmetadata: {name: new-crd}\n", false},
		{"StatefulSet with the wrong apiVersion", strings.Replace(good, "apiVersion: apps/v1", "apiVersion: apps/v1beta1", 1), false},
		{"other namespace", strings.Replace(good, "metadata: {name: new-config}", "metadata: {name: new-config, namespace: apps}", 1), false},
		{"generateName", strings.Replace(good, "metadata: {name: new-config}", "metadata: {name: new-config, generateName: new-}", 1), false},
		{"no name", strings.Replace(good, "metadata: {name: new-config}", "metadata: {labels: {a: b}}", 1), false},
		{"same object twice", good + "---\nkind: ConfigMap\nmetadata: {name: new-config}\n", false},
		{"duplicate YAML key", strings.Replace(good, "  replicas: 2\n", "  replicas: 2\n  replicas: 3\n", 1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.ok {
				scaledAccepted(t, "tempo", tc.body)
				return
			}
			scaledRefused(t, "tempo", tc.body, tc.name)
		})
	}
}

// Round 4 (flag spelling), now on the LIVE router: obs extends only a well-formed
// -storageNode flag it can read the way the store does.
func TestPortedLiveRouterFlagSpelling(t *testing.T) {
	const flag = `"-storageNode=vmstorage-0.vmstorage.obs-test.svc.cluster.local:8400"`
	for _, tc := range []struct{ name, to string }{
		{"dash removed (positional)", `"storageNode=a:8400"`},
		{"third dash", `"---storageNode=a:8400"`},
		{"after --", `"--", "-storageNode=a:8400"`},
		{"after a positional argument", `"run", "-storageNode=a:8400"`},
		{"no value", `"-storageNode"`},
		{"%{ENV} endpoint", `"-storageNode=a:8400,%{MORE}"`},
		{"empty endpoint", `"-storageNode=a:8400,,b:8400"`},
		{"two -storageNode flags", flag + `, "-storageNode=b:8400"`},
		{"no -storageNode flag", `"-x=1"`},
		{"derived endpoint already present (not by obs)", `"-storageNode=a:8400,` + vmEndpoint("vmstorage-b", 0, 8400) + `"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			live := vmLive("victoriametrics")
			if !strings.Contains(live, flag) {
				t.Fatal("fixture")
			}
			f := newFake(t, strings.Replace(live, flag, tc.to, 1))
			expectRefused(t, move(t, f, "victoriametrics", storeConfig("victoriametrics"), false), f.calls, tc.name)
		})
	}
	for _, tc := range []struct{ name, to string }{
		{"--storageNode= kept", `"--storageNode=a:8400"`},
		{"separate value kept", `"-storageNode", "a:8400"`},
	} {
		t.Run("control: "+tc.name, func(t *testing.T) {
			f := newFake(t, strings.Replace(vmLive("victoriametrics"), flag, tc.to, 1))
			if err := move(t, f, "victoriametrics", storeConfig("victoriametrics"), false); err != nil {
				t.Fatal(err)
			}
			a := liveArgs(f, "deployment.apps/vminsert")
			got := fmt.Sprint(a[3:])
			if !strings.HasPrefix(got, "["+strings.Trim(strings.Split(tc.to, ",")[0], `"`)) || !strings.Contains(got, "a:8400,"+vmEndpoint("vmstorage-b", 0, 8400)) {
				t.Fatalf("spelling not kept: %v", a)
			}
		})
	}
}

// Round 2 finding 1: a healthy multi-node Alloy DaemonSet (concept 10.3) has no
// spec.replicas / readyReplicas.
func TestPortedDaemonSetAlloy(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		desired, ready, updated int
		ok                      bool
	}{{"three healthy nodes", 3, 3, 3, true}, {"one node not ready", 3, 2, 3, false}, {"one node not updated", 3, 3, 2, false}, {"single node", 1, 1, 1, false}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t, clusterLive("tempo"))
			delete(f.objs, "deployment.apps/alloy")
			f.objs["daemonset.apps/alloy"] = map[string]any{"kind": "DaemonSet", "metadata": map[string]any{"name": "alloy"}, "status": map[string]any{"desiredNumberScheduled": float64(tc.desired), "numberReady": float64(tc.ready), "updatedNumberScheduled": float64(tc.updated)}}
			cfg := config()
			cfg.Alloy = "ds/alloy"
			err := f.kube().Execute(context.Background(), Reconnect, "tempo", cfg)
			if (err == nil) != tc.ok {
				t.Fatalf("ok=%v err=%v", tc.ok, err)
			}
		})
	}
}

// Round 1: each scaled component gets only its own roles; Kafka is never labelled; new
// roles are active before the single's go (make before break).
func TestPortedRolesPerComponent(t *testing.T) {
	cfg := config()
	cfg.Scaled = []string{"deployment/distributor", "deployment/query", "statefulset/kafka"}
	cfg.RoleWorkloads = map[string][]string{"deployment/distributor": {"obs.diene/tempo-write"}, "deployment/query": {"obs.diene/tempo-read"}}
	wl := func(kind, name string) string {
		return "apiVersion: apps/v1\nkind: " + kind + "\nmetadata: {name: " + name + "}\nspec: {replicas: 1, selector: {matchLabels: {component: " + name + "}}, template: {metadata: {labels: {component: " + name + "}}, spec: {containers: [{name: c, image: x}]}}}\n"
	}
	cfg.ScaledManifest = writeManifest(t, wl("Deployment", "distributor")+"---\n"+wl("Deployment", "query")+"---\n"+wl("StatefulSet", "kafka"))
	f := fakeFor(t, "tempo")
	if err := move(t, f, "tempo", cfg, false); err != nil {
		t.Fatal(err)
	}
	if c := callsWith(f.calls, "kafka", "label"); len(c) > 0 {
		t.Fatalf("Kafka labelled: %v", c)
	}
	if c := callsWith(f.calls, "statefulset.apps/kafka", "patch"); len(c) > 0 {
		t.Fatalf("Kafka patched: %v", c)
	}
	if l := f.podLabels("distributor-0"); l["obs.diene/tempo-write"] != "true" || l["obs.diene/tempo-read"] != "false" {
		t.Fatalf("distributor roles %v", l)
	}
	if l := f.podLabels("query-0"); l["obs.diene/tempo-read"] != "true" || l["obs.diene/tempo-write"] != "false" {
		t.Fatalf("query roles %v", l)
	}
	newTrue, singleFalse := -1, -1
	for i, c := range f.calls {
		if strings.HasPrefix(c, "label pods -l component=query") && strings.Contains(c, "=true") {
			newTrue = i
		}
		if strings.HasPrefix(c, "label pods -l component=single") && strings.Contains(c, "=false") && singleFalse < 0 {
			singleFalse = i
		}
	}
	if newTrue < 0 || singleFalse < 0 || newTrue > singleFalse {
		t.Fatalf("break before make: %v", f.calls)
	}
}

// Round 1 finding 3: the converted metastore's template carries the roles false, so a
// restart can never rejoin the write/query Services.
func TestPortedMetastoreTemplateRolesFalse(t *testing.T) {
	f := fakeFor(t, "pyroscope")
	if err := move(t, f, "pyroscope", storeConfig("pyroscope"), false); err != nil {
		t.Fatal(err)
	}
	tpl := stringMap(mapAt(f.objs["statefulset.apps/single"], "spec", "template", "metadata")["labels"])
	for _, r := range roles("pyroscope") {
		if tpl[r] != "false" {
			t.Fatalf("template %s=%s", r, tpl[r])
		}
	}
	f.reconcile("statefulset.apps/single") // a restart
	if l := f.podLabels("single-0"); l["obs.diene/pyroscope-write"] != "false" {
		t.Fatalf("restarted metastore rejoins: %v", l)
	}
}

// Brief item 5: objects print exact integers.
func TestScalarExactIntegers(t *testing.T) {
	for v, want := range map[any]string{float64(1000000): "1000000", float64(1e21): "1000000000000000000000", 2: "2", 0.5: "0.5"} {
		if got := scalar(v); got != want {
			t.Fatalf("scalar(%v) = %s, want %s", v, got, want)
		}
	}
}

// The JSON patch engine against RFC 6902 appendix A cases (it simulates the server
// in tests and replays stored inverses in production).
func TestJSONPatchRFC6902(t *testing.T) {
	doc := func(s string) map[string]any {
		var m map[string]any
		if err := json.Unmarshal([]byte(s), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	for _, tc := range []struct{ name, doc, patch, want string }{
		{"A.1 add object member", `{"foo":"bar"}`, `[{"op":"add","path":"/baz","value":"qux"}]`, `{"baz":"qux","foo":"bar"}`},
		{"A.2 add array element", `{"foo":["bar","baz"]}`, `[{"op":"add","path":"/foo/1","value":"qux"}]`, `{"foo":["bar","qux","baz"]}`},
		{"A.3 remove object member", `{"baz":"qux","foo":"bar"}`, `[{"op":"remove","path":"/baz"}]`, `{"foo":"bar"}`},
		{"A.4 remove array element", `{"foo":["bar","qux","baz"]}`, `[{"op":"remove","path":"/foo/1"}]`, `{"foo":["bar","baz"]}`},
		{"A.5 replace", `{"baz":"qux","foo":"bar"}`, `[{"op":"replace","path":"/baz","value":"boo"}]`, `{"baz":"boo","foo":"bar"}`},
		{"A.8 test success", `{"baz":"qux","foo":["a",2,"c"]}`, `[{"op":"test","path":"/baz","value":"qux"},{"op":"test","path":"/foo/1","value":2}]`, `{"baz":"qux","foo":["a",2,"c"]}`},
		{"A.9 test failure", `{"baz":"qux"}`, `[{"op":"test","path":"/baz","value":"bar"}]`, ``},
		{"A.10 add nested member", `{"foo":"bar"}`, `[{"op":"add","path":"/child","value":{"grandchild":{}}}]`, `{"foo":"bar","child":{"grandchild":{}}}`},
		{"A.12 add to nonexistent target", `{"foo":"bar"}`, `[{"op":"add","path":"/baz/bat","value":"qux"}]`, ``},
		{"A.14 ~ escape ordering", `{"/":9,"~1":10}`, `[{"op":"test","path":"/~01","value":10}]`, `{"/":9,"~1":10}`},
		{"A.16 add to array end", `{"foo":["bar"]}`, `[{"op":"add","path":"/foo/-","value":["abc","def"]}]`, `{"foo":["bar",["abc","def"]]}`},
		{"all or nothing", `{"a":1,"b":2}`, `[{"op":"replace","path":"/a","value":5},{"op":"test","path":"/b","value":3}]`, ``},
		{"replace absent member", `{"a":1}`, `[{"op":"replace","path":"/b","value":5}]`, ``},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var ops []op
			if err := json.Unmarshal([]byte(tc.patch), &ops); err != nil {
				t.Fatal(err)
			}
			in := doc(tc.doc)
			got, err := applyPatch(in, ops)
			if tc.want == "" {
				if err == nil {
					t.Fatalf("patch applied: %v", got)
				}
				if !reflect.DeepEqual(in, doc(tc.doc)) {
					t.Fatal("input modified by a failed patch")
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got, doc(tc.want)) {
				t.Fatalf("got %v err %v", got, err)
			}
			inv, err := invert(in, ops)
			if err != nil {
				t.Fatal(err)
			}
			back, err := applyPatch(got, inv)
			if err != nil || !reflect.DeepEqual(back, in) {
				t.Fatalf("inverse %v gives %v (%v)", inv, back, err)
			}
		})
	}
}

// ---- round 5 own attacks on the redesign ----

// Re-run after a rollback (scaled objects retained, roles cleared): the move resumes
// from Prepare, creates nothing, and ends in the same state as a first move.
func TestAttackForwardAfterRollback(t *testing.T) {
	for _, store := range []string{"tempo", "pyroscope", "victoriametrics"} {
		t.Run(store, func(t *testing.T) {
			clean := fakeFor(t, store)
			if err := move(t, clean, store, storeConfig(store), false); err != nil {
				t.Fatal(err)
			}
			f := fakeFor(t, store)
			for _, rollback := range []bool{false, true, false} {
				if err := move(t, f, store, storeConfig(store), rollback); err != nil {
					t.Fatalf("rollback=%v: %v", rollback, err)
				}
			}
			if len(f.created) != 1 {
				t.Fatalf("created %d times", len(f.created))
			}
			for res, obj := range clean.snapshot() {
				if !reflect.DeepEqual(f.snapshot()[res], obj) {
					t.Fatalf("%s differs at %s", res, firstDiff(f.snapshot()[res], obj))
				}
			}
		})
	}
}

// A second rollback changes no existing object; a rollback with nothing to roll back
// is refused before any change.
func TestAttackRollbackTwiceAndNothingToRollBack(t *testing.T) {
	for _, store := range []string{"tempo", "pyroscope", "victorialogs"} {
		t.Run(store, func(t *testing.T) {
			f := fakeFor(t, store)
			expectRefused(t, move(t, f, store, storeConfig(store), true), f.calls, "rollback before any move")
			for _, rollback := range []bool{false, true} {
				if err := move(t, f, store, storeConfig(store), rollback); err != nil {
					t.Fatal(err)
				}
			}
			f.calls = nil
			if err := move(t, f, store, storeConfig(store), true); err != nil {
				t.Fatal(err)
			}
			if p := callsWith(f.calls, "patch "); len(p) > 0 && !strings.Contains(p[0], "/new") {
				t.Fatalf("second rollback patched an existing object: %v", p)
			}
		})
	}
}

func TestAttackConfigRefusals(t *testing.T) {
	for _, tc := range []struct {
		name, store string
		mutate      func(*Config)
	}{
		{"Pyroscope single as a Deployment", "pyroscope", func(c *Config) { c.Single = "deployment/single" }},
		{"compactor is Alloy", "tempo", func(c *Config) { c.Compactor = c.Alloy }},
		{"compactor is a new workload", "tempo", func(c *Config) { c.Compactor = "statefulset/new" }},
		{"compactor is the single", "tempo", func(c *Config) { c.Compactor = c.Single }},
		{"router without port", "victoriametrics", func(c *Config) { c.Routers[0].Port = 0 }},
		{"router role unknown", "victoriametrics", func(c *Config) { c.Routers[0].Role = "storage" }},
		{"no select router", "victoriametrics", func(c *Config) { c.Routers = c.Routers[:1] }},
		{"same router twice", "victoriametrics", func(c *Config) { c.Routers[1].Workload = c.Routers[0].Workload }},
		{"storage_workloads without two copies", "victoriametrics", func(c *Config) { c.Storage = []string{"statefulset/vmstorage"} }},
		{"two copies without storage_workloads", "victoriametrics", func(c *Config) { c.Copies = 2 }},
		{"three copies", "victoriametrics", func(c *Config) { c.Copies = 3; c.Storage = []string{"statefulset/vmstorage"} }},
		{"single on a storage store", "victorialogs", func(c *Config) { c.Single = "statefulset/vlstorage" }},
		{"routers on Tempo", "tempo", func(c *Config) { c.Routers = []Router{{"deployment/x", "insert", 1}} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := storeConfig(tc.store)
			tc.mutate(&c)
			if err := c.Validate(tc.store, false); err == nil {
				t.Fatal("accepted")
			} else {
				t.Log(err)
			}
		})
	}
}

// A router or the single listed in scaled is an existing object: the scaled manifest
// would have to carry it, and it may not.
func TestAttackExistingObjectAsScaled(t *testing.T) {
	cfg := storeConfig("victoriametrics")
	cfg.Scaled = append(cfg.Scaled, "deployment/vminsert")
	cfg.ScaledManifest = writeManifest(t, storageScaled("victoriametrics", false)+"---\n"+strings.SplitN(vmLive("victoriametrics"), "---\n", 2)[0])
	f := fakeFor(t, "victoriametrics")
	expectRefused(t, move(t, f, "victoriametrics", cfg, false), f.calls, "router in scaled")
}
