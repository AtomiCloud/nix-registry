package storemove

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// kubeFixture serves `kubectl get <resource> -o json` from a per-resource body
// and records every argument line; unknown resources get a healthy 2/2 workload.
func kubeFixture(calls *[]string, bodies map[string]string) Kubernetes {
	return Kubernetes{Command: func(_ context.Context, args ...string) ([]byte, error) {
		s := strings.Join(args, " ")
		*calls = append(*calls, s)
		for resource, body := range bodies {
			if strings.Contains(s, "get "+resource+" ") {
				return []byte(body), nil
			}
		}
		name := "new"
		if strings.Contains(s, "single") {
			name = "single"
		}
		return []byte(fmt.Sprintf(`{"spec":{"replicas":2,"selector":{"matchLabels":{"component":"%s"}}},"status":{"readyReplicas":2}}`, name)), nil
	}}
}

func daemonSet(desired, ready, updated int) string {
	return fmt.Sprintf(`{"kind":"DaemonSet","spec":{"selector":{"matchLabels":{"app":"alloy"}}},"status":{"desiredNumberScheduled":%d,"numberReady":%d,"updatedNumberScheduled":%d}}`, desired, ready, updated)
}

// Review round 2, finding 1: a healthy multi-node Alloy DaemonSet (concept 10.3)
// has no spec.replicas / status.readyReplicas.
func TestReview2R1DaemonSetAlloy(t *testing.T) {
	cfg := config()
	cfg.Alloy = "ds/alloy"
	cases := []struct {
		name string
		body string
		ok   bool
	}{
		{"three healthy nodes", daemonSet(3, 3, 3), true},
		{"one node not ready", daemonSet(3, 2, 3), false},
		{"one node not updated", daemonSet(3, 3, 2), false},
		{"single node is not multi-node", daemonSet(1, 1, 1), false},
		{"no nodes scheduled", daemonSet(0, 0, 0), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls []string
			err := kubeFixture(&calls, map[string]string{"daemonset/alloy": tc.body}).Execute(context.Background(), Reconnect, "tempo", cfg)
			if tc.ok && err != nil {
				t.Fatalf("healthy DaemonSet refused: %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("unhealthy DaemonSet accepted: %v", calls)
			}
		})
	}
}

func TestReview2R1DeploymentAndStatefulSetAlloy(t *testing.T) {
	for _, kind := range []string{"deployment", "statefulset"} {
		for _, tc := range []struct {
			name         string
			replicas, up int
			ok           bool
		}{{"2 of 2", 2, 2, true}, {"1 of 2", 2, 1, false}, {"single replica", 1, 1, false}} {
			t.Run(kind+" "+tc.name, func(t *testing.T) {
				cfg := config()
				cfg.Alloy = kind + "/alloy"
				body := fmt.Sprintf(`{"spec":{"replicas":%d},"status":{"readyReplicas":%d}}`, tc.replicas, tc.up)
				var calls []string
				err := kubeFixture(&calls, map[string]string{kind + "/alloy": body}).Execute(context.Background(), Reconnect, "tempo", cfg)
				if (err == nil) != tc.ok {
					t.Fatalf("ok=%v err=%v", tc.ok, err)
				}
			})
		}
	}
}

func router(name, image, nodes string) string {
	return fmt.Sprintf("kind: Deployment\nmetadata: {name: %s}\nspec:\n  replicas: 2\n  template:\n    spec:\n      containers:\n        - name: c\n          image: %s\n          args: [\"-httpListenAddr=:8480\", \"-storageNode=%s\"]\n", name, image, nodes)
}

// Review round 2, finding 2: routing manifests may only carry the declared
// insert/select routers and -storageNode list changes.
func TestReview2R2RoutingManifestsOnlyRouters(t *testing.T) {
	original := reviewManifest(t, router("insert", "vm:1", "a:8400")+"---\n"+router("select", "vm:1", "a:8400"))
	good := reviewManifest(t, router("insert", "vm:1", "a:8400,b:8400")+"---\n"+router("select", "vm:1", "a:8400,b:8400"))
	alloy := "kind: Deployment\nmetadata: {name: alloy}\nspec: {replicas: 1}\n"
	cases := []struct {
		name, routing, original string
		step                    Step
		ok                      bool
	}{
		{"add nodes to both routers", good, original, StorageRoute, true},
		{"restore original", good, original, RestoreRoute, true},
		{"dedup flag on a router (concept 10.6 puts it on vmstorage only)", reviewManifest(t, strings.Replace(router("insert", "vm:1", "a:8400,b:8400"), "\"-storageNode", "\"-dedup.minScrapeInterval=1ms\", \"-storageNode", 1)), original, StorageRoute, false},
		{"alloy workload in routing", reviewManifest(t, router("insert", "vm:1", "a:8400,b:8400")+"---\n"+alloy), original, StorageRoute, false},
		{"app workload in routing", reviewManifest(t, alloy), original, StorageRoute, false},
		{"alloy workload in original", good, reviewManifest(t, router("insert", "vm:1", "a:8400")+"---\n"+alloy), RestoreRoute, false},
		{"router image changed", reviewManifest(t, router("insert", "vm:2", "a:8400,b:8400")), original, StorageRoute, false},
		{"original endpoint dropped", reviewManifest(t, router("insert", "vm:1", "b:8400")), original, StorageRoute, false},
		{"unrelated service", reviewManifest(t, router("insert", "vm:1", "a:8400,b:8400")+"---\nkind: Service\nmetadata: {name: other}\n"), original, StorageRoute, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config()
			cfg.RoutingManifest, cfg.OriginalRoutingManifest = tc.routing, tc.original
			var calls []string
			err := reviewKube(&calls).Execute(context.Background(), tc.step, "victoriametrics", cfg)
			if tc.ok && err != nil {
				t.Fatalf("declared router mutation refused: %v", err)
			}
			if !tc.ok && (err == nil || len(calls) != 0) {
				t.Fatalf("undeclared change applied: %v %v", err, calls)
			}
			if tc.ok && !strings.Contains(strings.Join(calls, "\n"), "apply -f") {
				t.Fatalf("nothing applied: %v", calls)
			}
		})
	}
}

// Review round 2, finding 4: the single's template role goes true while it has no
// pods (before scale-up), so the first Ready pod already carries the role.
func TestReview2R4RollbackPatchesSingleTemplateBeforeScaleUp(t *testing.T) {
	cfg := config()
	cfg.SingleManifest = reviewManifest(t, "kind: StatefulSet\nmetadata: {name: single}\nspec: {replicas: 1}\n")
	var calls []string
	k := reviewKube(&calls)
	for _, s := range Plan("tempo", true) {
		if s == Verify {
			continue
		}
		if err := k.Execute(context.Background(), s, "tempo", cfg); err != nil {
			t.Fatal(err)
		}
	}
	patch, scale, later := -1, -1, 0
	for i, c := range calls {
		if strings.Contains(c, "patch statefulset/single") && strings.Contains(c, `"obs.diene/tempo-read":"true"`) && strings.Contains(c, `"obs.diene/tempo-write":"true"`) {
			if patch < 0 {
				patch = i
			} else {
				later++
			}
		}
		if strings.Contains(c, "scale statefulset/single --replicas=1") {
			scale = i
		}
	}
	if patch < 0 || scale < 0 || patch >= scale {
		t.Fatalf("single template not patched true before scale-up (patch %d, scale %d): %v", patch, scale, calls)
	}
	if later != 0 {
		t.Fatalf("single template patched true again after it took traffic (restart): %v", calls)
	}
}
