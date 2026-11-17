package storemove

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// Round 4. Every plant below changes exactly one thing against a baseline that passes,
// so each refusal is caused by the guard it names (see the PR body for the red runs
// with each guard disabled).

// mutating reports the first call that would change the cluster (reads are allowed:
// the live checks run kubectl get before the first change).
func mutating(calls []string) string {
	for _, c := range calls {
		for _, verb := range []string{" apply ", " patch ", " scale ", " label ", " rollout restart ", " exec "} {
			if strings.Contains(c, verb) {
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

// ---- item 1: supporting objects are the store's own (kind AND name AND ownership) ----

func ownedScaled(store string) string {
	return "kind: StatefulSet\nmetadata: {name: new, labels: {obs.diene/store-move: " + store + "}}\nspec: {replicas: 1}\n"
}

func TestRound4ScaledSupportingObjects(t *testing.T) {
	cases := []struct {
		name, store, extra string
		live               map[string]string // resource -> live JSON
		ok                 bool
	}{
		{"baseline: own headless Service + ConfigMap, both new", "tempo", "kind: Service\nmetadata: {name: new-headless, labels: {obs.diene/store-move: tempo}}\nspec: {clusterIP: None, publishNotReadyAddresses: true, selector: {component: new}}\n---\nkind: ConfigMap\nmetadata: {name: new-config, labels: {obs.diene/store-move: tempo}}\ndata: {tempo.yaml: x}\n", nil, true},
		{"baseline: name derived from the single (gossip ring)", "tempo", "kind: Service\nmetadata: {name: single-gossip-ring, labels: {obs.diene/store-move: tempo}}\nspec: {clusterIP: None, publishNotReadyAddresses: true}\n", nil, true},
		{"re-run: own ConfigMap already created by the move", "tempo", "kind: ConfigMap\nmetadata: {name: new-config, labels: {obs.diene/store-move: tempo}}\ndata: {tempo.yaml: y}\n", map[string]string{"configmap/new-config": `{"kind":"ConfigMap","metadata":{"name":"new-config","labels":{"obs.diene/store-move":"tempo"}},"data":{"tempo.yaml":"x"}}`}, true},
		{"app ConfigMap name (checkout-env), otherwise valid", "tempo", "kind: ConfigMap\nmetadata: {name: checkout-env, labels: {obs.diene/store-move: tempo}}\ndata: {DATABASE_URL: wrong}\n", nil, false},
		{"app Service name (checkout), otherwise valid headless", "tempo", "kind: Service\nmetadata: {name: checkout, labels: {obs.diene/store-move: tempo}}\nspec: {clusterIP: None, publishNotReadyAddresses: true, selector: {app: unrelated}}\n", nil, false},
		{"own name but existing unowned ConfigMap (chart's)", "tempo", "kind: ConfigMap\nmetadata: {name: single-config, labels: {obs.diene/store-move: tempo}}\ndata: {a: b}\n", map[string]string{"configmap/single-config": `{"kind":"ConfigMap","metadata":{"name":"single-config"},"data":{"a":"live"}}`}, false},
		{"own name but existing unowned headless Service (single's)", "tempo", "kind: Service\nmetadata: {name: single-headless, labels: {obs.diene/store-move: tempo}}\nspec: {clusterIP: None, publishNotReadyAddresses: true, selector: {app: other}}\n", map[string]string{"service/single-headless": `{"kind":"Service","metadata":{"name":"single-headless"},"spec":{"clusterIP":"None","selector":{"component":"single"}}}`}, false},
		{"new object without the owner label", "tempo", "kind: ConfigMap\nmetadata: {name: new-config}\n", nil, false},
		{"owned live object, manifest drops the owner label", "tempo", "kind: ConfigMap\nmetadata: {name: new-config}\n", map[string]string{"configmap/new-config": `{"kind":"ConfigMap","metadata":{"name":"new-config","labels":{"obs.diene/store-move":"tempo"}}}`}, false},
		{"owned by another store's move", "tempo", "kind: ConfigMap\nmetadata: {name: new-config, labels: {obs.diene/store-move: tempo}}\n", map[string]string{"configmap/new-config": `{"kind":"ConfigMap","metadata":{"name":"new-config","labels":{"obs.diene/store-move":"pyroscope"}}}`}, false},
		{"non-headless Service", "tempo", "kind: Service\nmetadata: {name: new-svc, labels: {obs.diene/store-move: tempo}}\nspec: {publishNotReadyAddresses: true}\n", nil, false},
		{"Tempo headless Service without publishNotReadyAddresses", "tempo", "kind: Service\nmetadata: {name: new-headless, labels: {obs.diene/store-move: tempo}}\nspec: {clusterIP: None}\n", nil, false},
		{"headless Service of type LoadBalancer", "tempo", "kind: Service\nmetadata: {name: new-headless, labels: {obs.diene/store-move: tempo}}\nspec: {clusterIP: None, publishNotReadyAddresses: true, type: LoadBalancer}\n", nil, false},
		{"headless Service with externalIPs", "tempo", "kind: Service\nmetadata: {name: new-headless, labels: {obs.diene/store-move: tempo}}\nspec: {clusterIP: None, publishNotReadyAddresses: true, externalIPs: [1.2.3.4]}\n", nil, false},
		{"Secret", "tempo", "kind: Secret\nmetadata: {name: new-secret, labels: {obs.diene/store-move: tempo}}\n", nil, false},
		{"PVC", "tempo", "kind: PersistentVolumeClaim\nmetadata: {name: new-data, labels: {obs.diene/store-move: tempo}}\n", nil, false},
		{"RBAC outside Pyroscope", "tempo", "kind: ServiceAccount\nmetadata: {name: new-sa, labels: {obs.diene/store-move: tempo}}\n", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config()
			cfg.ScaledManifest = reviewManifest(t, ownedScaled(tc.store)+"---\n"+tc.extra)
			for r, body := range tc.live {
				setLiveJSON(t, r, body)
			}
			var calls []string
			err := reviewKube(&calls).Execute(context.Background(), Prepare, tc.store, cfg)
			if tc.ok {
				if err != nil {
					t.Fatalf("store's own object refused: %v", err)
				}
				if !strings.Contains(strings.Join(calls, "\n"), "apply -f") {
					t.Fatalf("nothing applied: %v", calls)
				}
				return
			}
			expectRefused(t, err, calls, tc.name)
		})
	}
}

const pyroRBAC = `kind: ServiceAccount
metadata: {name: new-metastore, labels: {obs.diene/store-move: pyroscope}}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata: {name: new-discovery, labels: {obs.diene/store-move: pyroscope}}
rules:
- apiGroups: [""]
  resources: [pods, endpoints]
  verbs: [get, list, watch]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata: {name: new-discovery, labels: {obs.diene/store-move: pyroscope}}
roleRef: {apiGroup: rbac.authorization.k8s.io, kind: Role, name: new-discovery}
subjects: [{kind: ServiceAccount, name: new-metastore}]
`

func TestRound4PyroscopeRBAC(t *testing.T) {
	for _, tc := range []struct {
		name, from, to string
		ok             bool
	}{
		{"baseline: kubernetes:/// discovery RBAC", "", "", true},
		{"Role reads secrets", "resources: [pods, endpoints]", "resources: [pods, endpoints, secrets]", false},
		{"Role may create pods", "verbs: [get, list, watch]", "verbs: [get, list, watch, create]", false},
		{"Role wildcard group", `apiGroups: [""]`, `apiGroups: ["*"]`, false},
		{"Role adds nonResourceURLs", "  verbs: [get, list, watch]\n", "  verbs: [get, list, watch]\n  nonResourceURLs: [/metrics]\n", false},
		{"RoleBinding to cluster-admin", "roleRef: {apiGroup: rbac.authorization.k8s.io, kind: Role, name: new-discovery}", "roleRef: {apiGroup: rbac.authorization.k8s.io, kind: ClusterRole, name: cluster-admin}", false},
		{"RoleBinding to a Role not in the manifest", "kind: Role, name: new-discovery}", "kind: Role, name: new-admin}", false},
		{"RoleBinding grants an app ServiceAccount", "subjects: [{kind: ServiceAccount, name: new-metastore}]", "subjects: [{kind: ServiceAccount, name: checkout}]", false},
		{"RoleBinding subject in another namespace", "subjects: [{kind: ServiceAccount, name: new-metastore}]", "subjects: [{kind: ServiceAccount, name: new-metastore, namespace: apps}]", false},
		{"RoleBinding grants a user", "subjects: [{kind: ServiceAccount, name: new-metastore}]", "subjects: [{kind: User, name: new-metastore}]", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config()
			cfg.RoleLabels = []string{"obs.diene/pyroscope-query", "obs.diene/pyroscope-write"}
			cfg.RoleWorkloads = map[string][]string{"statefulset/new": cfg.RoleLabels}
			body := pyroRBAC
			if tc.from != "" {
				if !strings.Contains(body, tc.from) {
					t.Fatalf("fixture lacks %q", tc.from)
				}
				body = strings.Replace(body, tc.from, tc.to, 1)
			}
			cfg.ScaledManifest = reviewManifest(t, ownedScaled("pyroscope")+"---\n"+body)
			var calls []string
			err := reviewKube(&calls).Execute(context.Background(), Prepare, "pyroscope", cfg)
			if tc.ok {
				if err != nil {
					t.Fatalf("baseline refused: %v", err)
				}
				return
			}
			expectRefused(t, err, calls, tc.name)
		})
	}
}

// Existing objects in scaled_manifest: only the VictoriaLogs/VictoriaMetrics storage
// workload may exist without the owner label, and only to gain replicas.
func TestRound4ScaledExistingStorageWorkload(t *testing.T) {
	const live = "kind: StatefulSet\nmetadata: {name: vmstorage, labels: {app: vmstorage}}\nspec:\n  replicas: 1\n  serviceName: vmstorage\n  persistentVolumeClaimRetentionPolicy: {whenDeleted: Retain, whenScaled: Retain}\n  template:\n    spec:\n      containers:\n        - name: s\n          image: vm:1\n          args: [\"-retentionPeriod=30d\"]\n"
	for _, tc := range []struct {
		name, store, from, to string
		ok                    bool
	}{
		{"baseline: storage nodes added (replicas 1 -> 2)", "victoriametrics", "replicas: 1", "replicas: 2", true},
		{"same replicas (re-run)", "victoriametrics", "", "", true},
		{"VictoriaLogs storage nodes added", "victorialogs", "replicas: 1", "replicas: 3", true},
		{"replicas and retention changed", "victoriametrics", "replicas: 1\n  serviceName: vmstorage\n  persistentVolumeClaimRetentionPolicy: {whenDeleted: Retain, whenScaled: Retain}", "replicas: 2\n  serviceName: vmstorage\n  persistentVolumeClaimRetentionPolicy: {whenDeleted: Retain, whenScaled: Delete}", false},
		{"replicas and args changed", "victoriametrics", "replicas: 1\n  serviceName", "replicas: 2\n  serviceName", false},
		{"owner label added to the existing workload", "victoriametrics", "labels: {app: vmstorage}", "labels: {app: vmstorage, obs.diene/store-move: victoriametrics}", false},
		{"retention policy omitted (last-applied would delete it)", "victoriametrics", "  persistentVolumeClaimRetentionPolicy: {whenDeleted: Retain, whenScaled: Retain}\n", "", false},
		{"Tempo may not touch an existing unowned workload", "tempo", "replicas: 1", "replicas: 2", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := vmConfig()
			cfg.Scaled = []string{"statefulset/vmstorage"}
			cfg.RoleWorkloads = map[string][]string{"statefulset/vmstorage": cfg.RoleLabels}
			setLive(t, true, reviewManifest(t, live))
			body := live
			if tc.from != "" {
				body = strings.Replace(live, tc.from, tc.to, 1)
				if tc.name == "replicas and args changed" {
					body = strings.Replace(body, "-retentionPeriod=30d", "-retentionPeriod=1d", 1)
				}
			}
			cfg.ScaledManifest = reviewManifest(t, body)
			var calls []string
			err := reviewKube(&calls).Execute(context.Background(), AddNodes, tc.store, cfg)
			if tc.ok {
				if err != nil {
					t.Fatalf("adding storage nodes refused: %v", err)
				}
				return
			}
			expectRefused(t, err, calls, tc.name)
		})
	}
	t.Run("replicas lowered", func(t *testing.T) {
		cfg := vmConfig()
		cfg.Scaled = []string{"statefulset/vmstorage"}
		setLive(t, true, reviewManifest(t, strings.Replace(live, "replicas: 1", "replicas: 3", 1)))
		cfg.ScaledManifest = reviewManifest(t, strings.Replace(live, "replicas: 1", "replicas: 2", 1))
		var calls []string
		expectRefused(t, reviewKube(&calls).Execute(context.Background(), AddNodes, "victoriametrics", cfg), calls, "replicas lowered")
	})
}

func TestRound4NewWorkloadNeverMountsExistingData(t *testing.T) {
	for name, volume := range map[string]string{
		"existing claim": "persistentVolumeClaim: {claimName: data-single-0}",
		"host path":      "hostPath: {path: /var/lib/single}",
	} {
		t.Run(name, func(t *testing.T) {
			cfg := config()
			cfg.ScaledManifest = reviewManifest(t, strings.Replace(ownedScaled("tempo"), "spec: {replicas: 1}\n", "spec:\n  replicas: 1\n  template:\n    spec:\n      volumes:\n        - name: d\n          "+volume+"\n", 1))
			var calls []string
			expectRefused(t, reviewKube(&calls).Execute(context.Background(), Prepare, "tempo", cfg), calls, name)
		})
	}
}

// ---- item 2: the single (metastore retain / rollback restore) against its live object ----

const pyroSingle = `apiVersion: apps/v1
kind: StatefulSet
metadata: {name: single, labels: {app: pyroscope}}
spec:
  replicas: 1
  serviceName: single-headless
  selector: {matchLabels: {component: single}}
  persistentVolumeClaimRetentionPolicy: {whenDeleted: Retain, whenScaled: Retain}
  template:
    metadata: {labels: {component: single, obs.diene/pyroscope-query: "true", obs.diene/pyroscope-write: "true"}}
    spec:
      containers:
      - name: pyroscope
        image: grafana/pyroscope:2.4.3
        args: ["-target=all", "-architecture.storage=v2", "-metastore.raft.server-id=single-0.single-headless.obs-test.svc.cluster.local:9099", "-metastore.raft.advertise-address=single-0.single-headless.obs-test.svc.cluster.local:9099"]
        volumeMounts: [{name: data, mountPath: /data}]
  volumeClaimTemplates:
  - metadata: {name: data}
    spec:
      accessModes: [ReadWriteOnce]
      resources: {requests: {storage: 1Gi}}
`

func pyroConfig() Config {
	cfg := config()
	cfg.RoleLabels = []string{"obs.diene/pyroscope-query", "obs.diene/pyroscope-write"}
	cfg.RoleWorkloads = map[string][]string{"statefulset/new": cfg.RoleLabels}
	return cfg
}

// asMetastore is the declared conversion: -target=metastore, role labels false.
func asMetastore(s string) string {
	s = strings.Replace(s, `"-target=all"`, `"-target=metastore"`, 1)
	return strings.Replace(s, `obs.diene/pyroscope-query: "true", obs.diene/pyroscope-write: "true"`, `obs.diene/pyroscope-query: "false", obs.diene/pyroscope-write: "false"`, 1)
}

// singlePlants are single-field changes of a single manifest; each must be refused.
var singlePlants = []struct{ name, from, to string }{
	{"replicas 0", "  replicas: 1\n", "  replicas: 0\n"},
	{"replicas 2", "  replicas: 1\n", "  replicas: 2\n"},
	{"PVC retention whenScaled Delete", "whenScaled: Retain", "whenScaled: Delete"},
	{"PVC retention whenDeleted Delete", "whenDeleted: Retain", "whenDeleted: Delete"},
	{"PVC retention policy nulled", "  persistentVolumeClaimRetentionPolicy: {whenDeleted: Retain, whenScaled: Retain}\n", "  persistentVolumeClaimRetentionPolicy: null\n"},
	{"PVC retention policy omitted (apply deletes a last-applied field)", "  persistentVolumeClaimRetentionPolicy: {whenDeleted: Retain, whenScaled: Retain}\n", ""},
	{"raft server id changed", "-metastore.raft.server-id=single-0", "-metastore.raft.server-id=other-0"},
	{"raft node id added", `"-architecture.storage=v2", `, `"-architecture.storage=v2", "-pyroscopedb.metastore.raft.node-id=different-identity", `},
	{"raft advertise address changed", "-metastore.raft.advertise-address=single-0", "-metastore.raft.advertise-address=x-0"},
	{"volumeClaimTemplates size", "storage: 1Gi", "storage: 2Gi"},
	{"volumeClaimTemplates name", "  - metadata: {name: data}\n", "  - metadata: {name: data2}\n"},
	{"data mount path", "mountPath: /data", "mountPath: /var/data"},
	{"volume mounts omitted (apply deletes a last-applied field)", "        volumeMounts: [{name: data, mountPath: /data}]\n", ""},
	{"image", "grafana/pyroscope:2.4.3", "grafana/pyroscope:9.9.9"},
	{"serviceName", "serviceName: single-headless", "serviceName: other"},
	{"extra field", "  serviceName: single-headless\n", "  serviceName: single-headless\n  revisionHistoryLimit: 99\n"},
	{"env added", "        image: grafana/pyroscope:2.4.3\n", "        image: grafana/pyroscope:2.4.3\n        env: [{name: X, value: y}]\n"},
	{"object label added", "labels: {app: pyroscope}", "labels: {app: pyroscope, extra: x}"},
	{"other template label", "component: single, obs", "component: single, extra: x, obs"},
	{"-target spelling changed", `"-target=`, `"--target=`},
	{"namespace elsewhere", "metadata: {name: single, labels: {app: pyroscope}}", "metadata: {name: single, namespace: apps, labels: {app: pyroscope}}"},
}

func TestRound4MetastoreAgainstLiveSingle(t *testing.T) {
	run := func(t *testing.T, manifest string, lastApplied bool) ([]string, error) {
		cfg := pyroConfig()
		setLive(t, lastApplied, reviewManifest(t, pyroSingle))
		cfg.MetastoreManifest = reviewManifest(t, manifest)
		if err := cfg.Validate("pyroscope", false); err != nil {
			t.Fatal(err)
		}
		var calls []string
		err := reviewKube(&calls).Execute(context.Background(), Step("retain single as metastore"), "pyroscope", cfg)
		return calls, err
	}
	base := asMetastore(pyroSingle)
	t.Run("baseline: -target=metastore and role labels false", func(t *testing.T) {
		if _, err := run(t, base, true); err != nil {
			t.Fatalf("declared conversion refused: %v", err)
		}
	})
	t.Run("baseline without last-applied (Helm-installed)", func(t *testing.T) {
		if _, err := run(t, base, false); err != nil {
			t.Fatalf("declared conversion refused: %v", err)
		}
	})
	t.Run("wrong target", func(t *testing.T) {
		calls, err := run(t, strings.Replace(base, `"-target=metastore"`, `"-target=all"`, 1), true)
		expectRefused(t, err, calls, "target all")
	})
	for _, p := range singlePlants {
		t.Run(p.name, func(t *testing.T) {
			if !strings.Contains(base, p.from) {
				t.Fatalf("fixture lacks %q", p.from)
			}
			calls, err := run(t, strings.Replace(base, p.from, p.to, 1), true)
			expectRefused(t, err, calls, p.name)
		})
	}
	t.Run("single not live", func(t *testing.T) {
		cfg := pyroConfig()
		cfg.MetastoreManifest = reviewManifest(t, base)
		var calls []string
		expectRefused(t, reviewKube(&calls).Execute(context.Background(), Step("retain single as metastore"), "pyroscope", cfg), calls, "absent single")
	})
}

func TestRound4RestoreAgainstLiveSingle(t *testing.T) {
	// Live: the retained metastore (or, for Tempo, the stopped single), last applied by
	// the move. The rollback manifest is the original single with role labels false.
	original := strings.Replace(pyroSingle, `obs.diene/pyroscope-query: "true", obs.diene/pyroscope-write: "true"`, `obs.diene/pyroscope-query: "false", obs.diene/pyroscope-write: "false"`, 1)
	run := func(t *testing.T, manifest string) ([]string, error) {
		cfg := pyroConfig()
		setLive(t, true, reviewManifest(t, asMetastore(pyroSingle)))
		cfg.SingleManifest = reviewManifest(t, manifest)
		var calls []string
		err := reviewKube(&calls).Execute(context.Background(), Restore, "pyroscope", cfg)
		return calls, err
	}
	t.Run("baseline: back to -target=all", func(t *testing.T) {
		if _, err := run(t, original); err != nil {
			t.Fatalf("declared restore refused: %v", err)
		}
	})
	t.Run("baseline: replicas may differ (applied as 0)", func(t *testing.T) {
		if _, err := run(t, strings.Replace(original, "  replicas: 1\n", "  replicas: 3\n", 1)); err != nil {
			t.Fatalf("restore with replicas 3 refused: %v", err)
		}
	})
	for _, p := range singlePlants {
		if strings.HasPrefix(p.name, "replicas") {
			continue
		}
		t.Run(p.name, func(t *testing.T) {
			calls, err := run(t, strings.Replace(original, p.from, p.to, 1))
			expectRefused(t, err, calls, p.name)
		})
	}
}

// ---- item 3: -storageNode spelling (Go flag syntax, VictoriaMetrics/VictoriaLogs envflag) ----

func TestRound4StorageFlagSpellingIsolated(t *testing.T) {
	spell := func(nodes, form string) string {
		return strings.Replace(router("insert", "vm:1", nodes), `"-storageNode=`+nodes+`"`, strings.ReplaceAll(form, "@", nodes), 1)
	}
	for _, tc := range []struct {
		name, fwd, orig string
		ok              bool
	}{
		{"baseline: -storageNode= endpoints added", spell("a:8400,b:8400", `"-storageNode=@"`), spell("a:8400", `"-storageNode=@"`), true},
		{"baseline: --storageNode= on both sides", spell("a:8400,b:8400", `"--storageNode=@"`), spell("a:8400", `"--storageNode=@"`), true},
		{"baseline: separate value on both sides", spell("a:8400,b:8400", `"-storageNode", "@"`), spell("a:8400", `"-storageNode", "@"`), true},
		{"dash removed (positional)", spell("a:8400,b:8400", `"storageNode=@"`), spell("a:8400", `"-storageNode=@"`), false},
		{"third dash", spell("a:8400,b:8400", `"---storageNode=@"`), spell("a:8400", `"-storageNode=@"`), false},
		{"second dash added (spelling changed)", spell("a:8400,b:8400", `"--storageNode=@"`), spell("a:8400", `"-storageNode=@"`), false},
		{"inline to separate value", spell("a:8400,b:8400", `"-storageNode", "@"`), spell("a:8400", `"-storageNode=@"`), false},
		{"original side positional", spell("a:8400,b:8400", `"-storageNode=@"`), spell("a:8400", `"storageNode=@"`), false},
		{"original side third dash", spell("a:8400,b:8400", `"-storageNode=@"`), spell("a:8400", `"---storageNode=@"`), false},
		{"after -- (positional)", spell("a:8400,b:8400", `"--", "-storageNode=@"`), spell("a:8400", `"--", "-storageNode=@"`), false},
		{"after a positional argument", spell("a:8400,b:8400", `"run", "-storageNode=@"`), spell("a:8400", `"run", "-storageNode=@"`), false},
		{"no value", strings.Replace(router("insert", "vm:1", "x"), `"-storageNode=x"`, `"-storageNode"`, 1), strings.Replace(router("insert", "vm:1", "x"), `"-storageNode=x"`, `"-storageNode"`, 1), false},
		{"%{ENV} endpoint", spell("a:8400,%{MORE}", `"-storageNode=@"`), spell("a:8400", `"-storageNode=@"`), false},
		{"empty endpoint", spell("a:8400,,b:8400", `"-storageNode=@"`), spell("a:8400", `"-storageNode=@"`), false},
	} {
		for _, step := range []Step{StorageRoute, RestoreRoute} {
			t.Run(tc.name+"/"+string(step), func(t *testing.T) {
				cfg := config()
				cfg.Routers = []string{"deployment/insert"}
				cfg.RoutingManifest = reviewManifest(t, tc.fwd)
				cfg.OriginalRoutingManifest = reviewManifest(t, tc.orig)
				if tc.ok {
					setLive(t, true, cfg.OriginalRoutingManifest)
				}
				var calls []string
				err := reviewKube(&calls).Execute(context.Background(), step, "victoriametrics", cfg)
				if tc.ok {
					if err != nil {
						t.Fatalf("allowed spelling refused: %v", err)
					}
					return
				}
				if err == nil || len(calls) != 0 {
					t.Fatalf("spelling %s accepted or refused after kubectl ran: err=%v calls=%v", tc.name, err, calls)
				}
			})
		}
	}
}

// ---- own attack: routing manifests against the live routers ----

func TestRound4RoutingAgainstLive(t *testing.T) {
	orig := router("insert", "vm:1", "a:8400") + "---\n" + router("select", "vm:1", "a:8400")
	fwd := router("insert", "vm:1", "a:8400,b:8400") + "---\n" + router("select", "vm:1", "a:8400,b:8400")
	both := func(s, from, to string) string { return strings.ReplaceAll(s, from, to) }
	for _, tc := range []struct {
		name, fwd, orig, live string
		step                  Step
		ok                    bool
	}{
		{"baseline: forward from live original", fwd, orig, orig, StorageRoute, true},
		{"baseline: re-run after the forward apply", fwd, orig, fwd, StorageRoute, true},
		{"baseline: rollback from live forward", fwd, orig, fwd, RestoreRoute, true},
		{"baseline: rollback already restored", fwd, orig, orig, RestoreRoute, true},
		{"same extra field in both files, not live", both(fwd, "  replicas: 2\n", "  replicas: 2\n  revisionHistoryLimit: 99\n"), both(orig, "  replicas: 2\n", "  replicas: 2\n  revisionHistoryLimit: 99\n"), orig, StorageRoute, false},
		{"same extra field in both files, rollback", both(fwd, "  replicas: 2\n", "  replicas: 2\n  revisionHistoryLimit: 99\n"), both(orig, "  replicas: 2\n", "  replicas: 2\n  revisionHistoryLimit: 99\n"), fwd, RestoreRoute, false},
		{"same image change in both files", both(fwd, "vm:1", "vm:2"), both(orig, "vm:1", "vm:2"), orig, StorageRoute, false},
		{"same replicas change in both files", both(fwd, "replicas: 2", "replicas: 0"), both(orig, "replicas: 2", "replicas: 0"), orig, StorageRoute, false},
		{"original lies about the live endpoints", fwd, orig, both(orig, "a:8400", "z:8400"), StorageRoute, false},
		{"both files omit a last-applied field", fwd, orig, both(orig, "  replicas: 2\n", "  replicas: 2\n  minReadySeconds: 5\n"), StorageRoute, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config()
			cfg.RoutingManifest, cfg.OriginalRoutingManifest = reviewManifest(t, tc.fwd), reviewManifest(t, tc.orig)
			setLive(t, true, reviewManifest(t, tc.live))
			var calls []string
			err := reviewKube(&calls).Execute(context.Background(), tc.step, "victoriametrics", cfg)
			if tc.ok {
				if err != nil {
					t.Fatalf("baseline refused: %v", err)
				}
				return
			}
			expectRefused(t, err, calls, tc.name)
		})
	}
	t.Run("router not live", func(t *testing.T) {
		cfg := config()
		cfg.RoutingManifest, cfg.OriginalRoutingManifest = reviewManifest(t, fwd), reviewManifest(t, orig)
		var calls []string
		expectRefused(t, reviewKube(&calls).Execute(context.Background(), StorageRoute, "victoriametrics", cfg), calls, "absent router")
	})
}

// ---- own attack: object shapes kubectl reads differently ----

func TestRound4ManifestShapes(t *testing.T) {
	good := ownedScaled("tempo")
	for _, tc := range []struct {
		name, body string
		ok         bool
	}{
		{"baseline", good, true},
		{"baseline inside nested Lists", "apiVersion: v1\nkind: List\nitems:\n  - apiVersion: v1\n    kind: List\n    items:\n      - kind: StatefulSet\n        metadata: {name: new, labels: {obs.diene/store-move: tempo}}\n        spec: {replicas: 1}\n", true},
		{"items smuggled on a StatefulSet (kubectl applies them as a list)", strings.TrimSuffix(good, "\n") + "\nitems:\n  - kind: Service\n    metadata: {name: tempo-query}\n    spec: {selector: {app: unrelated}}\n", false},
		{"typed list kind", "kind: ServiceList\nitems:\n  - " + strings.ReplaceAll(strings.TrimSuffix(good, "\n"), "\n", "\n    ") + "\n", false},
		{"List with another apiVersion", "apiVersion: v2\nkind: List\nitems:\n  - " + strings.ReplaceAll(strings.TrimSuffix(good, "\n"), "\n", "\n    ") + "\n", false},
		{"lower-case kind", strings.Replace(good, "kind: StatefulSet", "kind: statefulset", 1), false},
		{"alias kind", strings.Replace(good, "kind: StatefulSet", "kind: sts", 1), false},
		{"CRD reusing a Kind name", good + "---\napiVersion: example.com/v1\nkind: Service\nmetadata: {name: new-headless, labels: {obs.diene/store-move: tempo}}\nspec: {clusterIP: None, publishNotReadyAddresses: true}\n", false},
		{"StatefulSet with the wrong apiVersion", "apiVersion: apps/v1beta1\n" + good, false},
		{"other namespace", strings.Replace(good, "metadata: {name: new,", "metadata: {name: new, namespace: apps,", 1), false},
		{"generateName", good + "---\nkind: ConfigMap\nmetadata: {generateName: new-, name: new-config, labels: {obs.diene/store-move: tempo}}\n", false},
		{"no name", good + "---\nkind: ConfigMap\nmetadata: {labels: {obs.diene/store-move: tempo}}\n", false},
		{"same object twice", good + "---\n" + good, false},
		{"duplicate YAML key", strings.Replace(good, "spec: {replicas: 1}", "spec: {replicas: 1, replicas: 0}", 1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config()
			cfg.ScaledManifest = reviewManifest(t, tc.body)
			var calls []string
			err := reviewKube(&calls).Execute(context.Background(), Prepare, "tempo", cfg)
			if tc.ok {
				if err != nil {
					t.Fatalf("baseline refused: %v", err)
				}
				return
			}
			if err == nil || len(calls) != 0 {
				t.Fatalf("%s accepted or refused after kubectl ran: err=%v calls=%v", tc.name, err, calls)
			}
		})
	}
}

// The live fake serves server-shaped JSON; a guard against a silently broken fixture.
func TestRound4LiveFixtureIsServerShaped(t *testing.T) {
	setLive(t, true, reviewManifest(t, pyroSingle))
	var calls []string
	l, found, err := reviewKube(&calls).live(context.Background(), config(), object{Kind: "StatefulSet", Name: "single"})
	if err != nil || !found {
		t.Fatalf("live fixture missing: %v", err)
	}
	b, _ := json.Marshal(l)
	for _, want := range []string{`"uid"`, `"status"`, `"terminationMessagePath"`, lastAppliedAnnotation} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("live fixture lacks %s: %s", want, b)
		}
	}
	if len(calls) != 1 || !strings.Contains(calls[0], "get statefulset.apps/single -o json --ignore-not-found") {
		t.Fatalf("live read argv: %v", calls)
	}
}

// A live single whose claims would be deleted on scale-down is refused even when the
// manifest leaves the policy alone (the rollback applies it at replicas 0).
func TestRound4LiveDeleteRetentionRefused(t *testing.T) {
	const policy = "  persistentVolumeClaimRetentionPolicy: {whenDeleted: Retain, whenScaled: Retain}\n"
	for _, key := range []string{"whenScaled", "whenDeleted"} {
		liveBody := strings.Replace(pyroSingle, key+": Retain", key+": Delete", 1)
		for _, tc := range []struct {
			name, manifest string
			step           Step
		}{
			{"metastore", asMetastore(strings.Replace(pyroSingle, policy, "", 1)), Step("retain single as metastore")},
			{"restore", strings.Replace(pyroSingle, policy, "", 1), Restore},
		} {
			t.Run(key+"/"+tc.name, func(t *testing.T) {
				cfg := pyroConfig()
				live := liveBody
				if tc.step == Restore {
					live = asMetastore(liveBody)
				}
				setLive(t, false, reviewManifest(t, live))
				cfg.MetastoreManifest = reviewManifest(t, tc.manifest)
				cfg.SingleManifest = reviewManifest(t, strings.Replace(tc.manifest, `"true"`, `"false"`, 2))
				var calls []string
				expectRefused(t, reviewKube(&calls).Execute(context.Background(), tc.step, "pyroscope", cfg), calls, key+" Delete live")
			})
		}
	}
	t.Run("control: same manifests, live Retain", func(t *testing.T) {
		cfg := pyroConfig()
		setLive(t, false, reviewManifest(t, pyroSingle))
		cfg.MetastoreManifest = reviewManifest(t, asMetastore(strings.Replace(pyroSingle, policy, "", 1)))
		var calls []string
		if err := reviewKube(&calls).Execute(context.Background(), Step("retain single as metastore"), "pyroscope", cfg); err != nil {
			t.Fatalf("control refused: %v", err)
		}
	})
}
