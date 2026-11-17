package storemove

import (
	"context"
	"strings"
	"testing"
)

func TestCorrelation2RoutingPreservesEachContainerEndpoints(t *testing.T) {
	original := router("insert", "vm:1", "a:8400") + "        - name: sidecar\n          image: vm:1\n          args: [\"-storageNode=x:8400\"]\n"
	changed := router("insert", "vm:1", "b:8400") + "        - name: sidecar\n          image: vm:1\n          args: [\"-storageNode=a:8400,x:8400\"]\n"
	cfg := config()
	cfg.OriginalRoutingManifest = reviewManifest(t, original)
	cfg.RoutingManifest = reviewManifest(t, changed)
	var calls []string
	err := reviewKube(&calls).Execute(context.Background(), StorageRoute, "victoriametrics", cfg)
	if err == nil || len(calls) != 0 {
		t.Fatalf("routing.yaml: insert main container drops a:8400 by moving it into sidecar; accepted: err=%v calls=%v", err, calls)
	}
}

func TestCorrelation2RoutingChecksOriginalOnlyRouter(t *testing.T) {
	cfg := config()
	cfg.RoutingManifest = reviewManifest(t, router("insert", "vm:1", "a:8400,b:8400"))
	cfg.OriginalRoutingManifest = reviewManifest(t, router("insert", "vm:1", "a:8400")+"---\n"+router("select", "broken-image", "lost:8400"))
	var calls []string
	err := reviewKube(&calls).Execute(context.Background(), RestoreRoute, "victoriametrics", cfg)
	if err == nil || len(calls) != 0 {
		t.Fatalf("original.yaml: select has no forward counterpart, so image/endpoints cannot be checked; applied: err=%v calls=%v", err, calls)
	}
}

func TestCorrelation2RoutingRejectsArbitraryDedup(t *testing.T) {
	cfg := config()
	cfg.OriginalRoutingManifest = reviewManifest(t, router("insert", "vm:1", "a:8400"))
	cfg.RoutingManifest = reviewManifest(t, strings.Replace(router("insert", "vm:1", "a:8400,b:8400"), "\"-storageNode", "\"-dedup.minScrapeInterval=1h\", \"-storageNode", 1))
	var calls []string
	err := reviewKube(&calls).Execute(context.Background(), StorageRoute, "victoriametrics", cfg)
	if err == nil || len(calls) != 0 {
		t.Fatalf("routing.yaml: undocumented dedup.minScrapeInterval=1h accepted: err=%v calls=%v", err, calls)
	}
}

func TestCorrelation2RestoreDoesNotStartBeforeRoles(t *testing.T) {
	cfg := config()
	cfg.SingleManifest = reviewManifest(t, "kind: StatefulSet\nmetadata: {name: single}\nspec:\n  replicas: 1\n  template:\n    metadata:\n      labels: {obs.diene/tempo-read: 'false', obs.diene/tempo-write: 'false'}\n")
	setLive(t, true, cfg.SingleManifest)
	var calls []string
	base := reviewKube(&calls)
	roles := false
	k := Kubernetes{Command: func(ctx context.Context, args ...string) ([]byte, error) {
		line := strings.Join(args, " ")
		if strings.Contains(line, "apply -f "+cfg.SingleManifest) && !roles {
			t.Error("single.yaml: apply replicas:1 starts the single with role=false before template-role patch")
		}
		if strings.Contains(line, "patch statefulset/single") && strings.Contains(line, `"obs.diene/tempo-read":"true"`) {
			roles = true
		}
		return base.Command(ctx, args...)
	}}
	if err := k.Execute(context.Background(), Restore, "tempo", cfg); err != nil {
		t.Fatal(err)
	}
}
