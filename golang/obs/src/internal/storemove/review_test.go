package storemove

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func reviewManifest(t *testing.T, body string) string {
	t.Helper()
	f := filepath.Join(t.TempDir(), "manifest.yaml")
	if err := os.WriteFile(f, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return f
}
func reviewKube(calls *[]string) Kubernetes {
	return Kubernetes{Command: func(_ context.Context, args ...string) ([]byte, error) {
		s := strings.Join(args, " ")
		*calls = append(*calls, s)
		if strings.Contains(s, "jsonpath") {
			return []byte("2"), nil
		}
		name := "new"
		if strings.Contains(s, "single") {
			name = "single"
		}
		return []byte(fmt.Sprintf(`{"spec":{"replicas":2,"selector":{"matchLabels":{"component":"%s"}}},"status":{"readyReplicas":2}}`, name)), nil
	}}
}
func TestReview1TempoRealRangeResponse(t *testing.T) {
	start := time.Unix(1700000040, 0)
	var samples []string
	for i := 0; i < 60; i++ {
		samples = append(samples, fmt.Sprintf(`{"timestampMs":"%d","value":%d}`, start.Add(time.Duration(i)*time.Minute).UnixMilli(), i+1))
	}
	body := `{"series":[{"samples":[` + strings.Join(samples, ",") + `]}]}`
	got, err := httpFixture(body, nil).Counts(context.Background(), "tempo", config(), start.Add(59*time.Minute))
	if err != nil || len(got) != 60 || got[17] != 18 {
		t.Fatalf("real Tempo range refused: %v %v", got, err)
	}
}
func TestReview2RollbackClearsScaledTemplates(t *testing.T) {
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
	found := false
	for _, c := range calls {
		if strings.Contains(c, "patch statefulset/new") && strings.Contains(c, `"obs.diene/tempo-read":"false"`) {
			found = true
		}
	}
	if !found {
		t.Fatalf("rollback leaves template active: %v", calls)
	}
}
func TestReview2RollbackChecksScaledReceivedFlat(t *testing.T) {
	c := &fakeCluster{}
	m := &fakeMetrics{counters: map[string][]float64{received("tempo"): {1, 2}}}
	err := runFake(c, m).Run(context.Background(), "tempo", config(), true, false)
	if err == nil || !strings.Contains(err.Error(), received("tempo")) {
		t.Fatalf("reverse traffic not checked: %v", err)
	}
}
func TestReview3MetastoreCannotRetainServiceRoles(t *testing.T) {
	cfg := config()
	cfg.MetastoreManifest = reviewManifest(t, "kind: StatefulSet\nmetadata: {name: single}\nspec:\n  replicas: 1\n  template:\n    metadata:\n      labels: {obs.diene/tempo-read: true}\n")
	var calls []string
	err := reviewKube(&calls).Execute(context.Background(), Step("retain single as metastore"), "pyroscope", cfg)
	if err == nil || len(calls) != 0 {
		t.Fatalf("active metastore role accepted: %v %v", err, calls)
	}
	cfg.MetastoreManifest = reviewManifest(t, "kind: StatefulSet\nmetadata: {name: single}\nspec: {replicas: 1}\n")
	calls = nil
	if err = reviewKube(&calls).Execute(context.Background(), Step("retain single as metastore"), "pyroscope", cfg); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range calls {
		if strings.Contains(c, "patch statefulset/single") && strings.Contains(c, `"obs.diene/tempo-read":"false"`) {
			found = true
		}
	}
	if !found {
		t.Fatal("metastore template was never cleared")
	}
}
func TestReview5HeadlessServiceAllowed(t *testing.T) {
	cfg := config()
	cfg.ScaledManifest = reviewManifest(t, "kind: StatefulSet\nmetadata: {name: new}\nspec: {replicas: 1}\n---\nkind: Service\nmetadata: {name: tempo-headless}\nspec: {clusterIP: None, publishNotReadyAddresses: true}\n")
	if err := validateScaledManifest(cfg); err != nil {
		t.Fatal(err)
	}
}
func TestReview5ProtectedEndpointsAlloyAndAliases(t *testing.T) {
	for _, body := range []string{"kind: EndpointSlice\nmetadata:\n  name: tempo-query-fixture\n  labels: {kubernetes.io/service-name: tempo-query}\n", "kind: ConfigMap\nmetadata: {name: alloy}\n"} {
		t.Run(strings.Split(body, "\n")[0], func(t *testing.T) {
			cfg := config()
			cfg.RoutingManifest = reviewManifest(t, body)
			var calls []string
			err := reviewKube(&calls).Execute(context.Background(), StorageRoute, "victoriametrics", cfg)
			if err == nil || len(calls) != 0 {
				t.Fatalf("protected object applied: %v %v", err, calls)
			}
		})
	}
	cfg := config()
	cfg.Single = "sts/single"
	cfg.Scaled = append(cfg.Scaled, "statefulset/single")
	cfg.ScaledManifest = reviewManifest(t, "kind: StatefulSet\nmetadata: {name: new}\nspec: {replicas: 1}\n---\nkind: StatefulSet\nmetadata: {name: single}\nspec: {replicas: 1}\n")
	if err := validateScaledManifest(cfg); err == nil || !strings.Contains(err.Error(), "old single") {
		t.Fatalf("kind alias bypassed old-single protection: %v", err)
	}
}
func TestReview8RolesMakeBeforeBreak(t *testing.T) {
	cfg := config()
	var calls []string
	if err := reviewKube(&calls).roles(context.Background(), cfg, false); err != nil {
		t.Fatal(err)
	}
	target, source := -1, -1
	for i, c := range calls {
		if strings.Contains(c, "label pods -l component=new") && strings.Contains(c, "=true") {
			target = i
		}
		if strings.Contains(c, "label pods -l component=single") && strings.Contains(c, "=false") {
			source = i
		}
	}
	if target < 0 || source < 0 || target >= source {
		t.Fatalf("break before make: %v", calls)
	}
}
