package check

import (
	"github.com/AtomiCloud/obs/internal/engines/vm"
	"github.com/AtomiCloud/obs/internal/seed"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCorrelationKeptScopeEngineControl(t *testing.T) {
	p := filepath.Join(t.TempDir(), "seed.yaml")
	if err := os.WriteFile(p, []byte(dottedSeed), 0600); err != nil {
		t.Fatal(err)
	}
	sd, err := seed.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	ds, err := seed.Build(sd, time.Now().UTC().Truncate(time.Minute).Add(-sd.Duration()-10*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	e, err := vm.New(ds)
	if err != nil {
		t.Fatal(err)
	}
	const made = `label_set(checkout_charges_total, "payment_provider", "review")`
	exprs := []string{
		`sum by (payment_provider) (topk by (outcome) (2, ` + made + `))`,
		`sum by (payment_provider) (topk without (payment_provider) (2, ` + made + `))`,
		`sum by (payment_provider) (` + made + ` and on (outcome) checkout_charges_total)`,
		`sum by (payment_provider) (` + made + ` or on (outcome) checkout_charges_total)`,
		`sum by (payment_provider) (` + made + ` unless on (outcome) checkout_charges_total{outcome="not_present"})`,
	}
	for _, expr := range exprs {
		rs, err := e.Instant(expr, ds.End)
		if err != nil || len(rs) != 1 {
			t.Errorf("engine expr=%s rows=%+v err=%v", expr, rs, err)
			continue
		}
		found := false
		for _, l := range rs[0].Labels {
			if l.Name == "payment_provider" && l.Value == "review" {
				found = true
			}
		}
		if !found {
			t.Errorf("engine dropped generated label: %s: %+v", expr, rs)
			continue
		}
		t.Logf("engine kept payment_provider=review: %s: %+v", expr, rs)
	}
	e.Close()
	for _, expr := range exprs {
		if r := dottedLive(t, expr); r.Failures() != 0 {
			t.Errorf("engine-kept generated label falsely rejected: %s: %+v", expr, r.Findings)
		}
	}
}
