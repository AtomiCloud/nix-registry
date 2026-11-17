package storemove

import (
	"context"
	"strings"
	"testing"
)

func TestReviewer4FourAllowListPlants(t *testing.T) {
	for _, step := range []Step{StorageRoute, RestoreRoute} {
		for _, plant := range []string{"extra-router-field", "alloy-in-list", "service-selector", "reverse-storageNode-removal"} {
			t.Run(string(step)+"/"+plant, func(t *testing.T) {
				cfg := config()
				old := router("insert", "vm:1", "a:8400")
				next := router("insert", "vm:1", "a:8400,b:8400")
				switch plant {
				case "extra-router-field":
					next = strings.Replace(next, "  replicas: 2", "  replicas: 2\n  revisionHistoryLimit: 99", 1)
				case "alloy-in-list":
					next += "---\napiVersion: v1\nkind: List\nitems:\n  - kind: Deployment\n    metadata: {name: alloy}\n    spec: {replicas: 1}\n"
				case "service-selector":
					next += "---\napiVersion: v1\nkind: Service\nmetadata: {name: vm-insert}\nspec: {selector: {app: unrelated}}\n"
				case "reverse-storageNode-removal":
					old = strings.Replace(old, "a:8400", "lost:8400", 1)
				}
				cfg.OriginalRoutingManifest, cfg.RoutingManifest = reviewManifest(t, old), reviewManifest(t, next)
				var calls []string
				err := reviewKube(&calls).Execute(context.Background(), step, "victoriametrics", cfg)
				if err == nil || len(calls) != 0 {
					t.Fatalf("plant %s accepted: %v %v", plant, err, calls)
				}
				t.Log(err)
			})
		}
	}
}

func TestReviewer4NonRoutingExistingObjects(t *testing.T) {
	for _, step := range []Step{Prepare, Restore, Step("retain single as metastore")} {
		for _, extra := range []string{
			"kind: ConfigMap\nmetadata: {name: checkout-env}\ndata: {DATABASE_URL: 'postgres://wrong-db'}\n",
			"kind: Service\nmetadata: {name: checkout}\nspec: {selector: {app: unrelated}}\n",
			"kind: RoleBinding\nmetadata: {name: checkout-admin}\nroleRef: {apiGroup: rbac.authorization.k8s.io, kind: ClusterRole, name: cluster-admin}\nsubjects: [{kind: ServiceAccount, name: checkout}]\n",
		} {
			t.Run(string(step)+"/"+strings.Split(extra, "\n")[0], func(t *testing.T) {
				cfg := config()
				obj := "kind: StatefulSet\nmetadata: {name: single}\nspec: {replicas: 1}\n"
				if step == Prepare {
					obj = strings.Replace(obj, "name: single", "name: new", 1)
				}
				f := reviewManifest(t, obj+"---\n"+extra)
				cfg.ScaledManifest, cfg.SingleManifest, cfg.MetastoreManifest = f, f, f
				var calls []string
				err := reviewKube(&calls).Execute(context.Background(), step, "tempo", cfg)
				if err == nil {
					t.Fatalf("unrelated existing object applied: %s calls=%v", extra, calls)
				}
			})
		}
	}
}

func TestReviewer4StorageFlagSpelling(t *testing.T) {
	for _, spelling := range []string{"storageNode=", "---storageNode="} {
		for _, step := range []Step{StorageRoute, RestoreRoute} {
			t.Run(spelling+"/"+string(step), func(t *testing.T) {
				cfg := config()
				cfg.OriginalRoutingManifest = reviewManifest(t, router("insert", "vm:1", "a:8400"))
				cfg.RoutingManifest = reviewManifest(t, strings.Replace(router("insert", "vm:1", "a:8400,b:8400"), "-storageNode=", spelling, 1))
				var calls []string
				err := reviewKube(&calls).Execute(context.Background(), step, "victoriametrics", cfg)
				if err == nil {
					t.Fatalf("router flag spelling %q accepted, calls=%v", spelling, calls)
				}
			})
		}
	}
}

func TestReviewer4MetastoreRetainsStorage(t *testing.T) {
	cfg := config()
	cfg.RoleLabels = []string{"obs.diene/pyroscope-query", "obs.diene/pyroscope-write"}
	cfg.RoleWorkloads = map[string][]string{"statefulset/new": cfg.RoleLabels}
	cfg.MetastoreManifest = reviewManifest(t, `apiVersion: apps/v1
kind: StatefulSet
metadata: {name: single}
spec:
  replicas: 0
  serviceName: metastore
  selector: {matchLabels: {component: single}}
  persistentVolumeClaimRetentionPolicy: {whenDeleted: Delete, whenScaled: Delete}
  template:
    metadata: {labels: {component: single}}
    spec:
      containers:
      - name: metastore
        image: grafana/pyroscope:2.4.3
        args: ["-target=metastore", "-pyroscopedb.metastore.raft.node-id=different-identity"]
        volumeMounts: [{name: data, mountPath: /data}]
  volumeClaimTemplates:
  - metadata: {name: data}
    spec:
      accessModes: [ReadWriteOnce]
      resources: {requests: {storage: 1Gi}}
`)
	if err := cfg.Validate("pyroscope", false); err != nil {
		t.Fatal(err)
	}
	var calls []string
	err := reviewKube(&calls).Execute(context.Background(), Step("retain single as metastore"), "pyroscope", cfg)
	if err == nil {
		t.Fatalf("metastore manifest scales existing StatefulSet to zero with Delete retention and changes raft identity; applied: %v", calls)
	}
}

func TestReviewer4IsolateDedupGuard(t *testing.T) {
	const keep = `"-retentionPeriod=1"`
	const dedup = `, "-dedup.minScrapeInterval=1ms"`
	for _, tc := range []struct{ name, old, next string }{
		{"removal-only", keep + dedup, keep},
		{"duplicate-only", keep, keep + dedup + dedup},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := vmConfig()
			cfg.RoutingManifest = reviewManifest(t, router("insert", "vm:1", "a:8400,b:8400")+"---\n"+vmstorage(tc.next))
			cfg.OriginalRoutingManifest = reviewManifest(t, router("insert", "vm:1", "a:8400")+"---\n"+vmstorage(tc.old))
			var calls []string
			err := reviewKube(&calls).Execute(context.Background(), StorageRoute, "victoriametrics", cfg)
			if err == nil || len(calls) != 0 {
				t.Fatalf("dedup %s accepted: %v %v", tc.name, err, calls)
			}
		})
	}
}
