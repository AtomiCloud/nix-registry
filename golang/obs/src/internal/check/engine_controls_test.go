package check

import (
	"github.com/AtomiCloud/obs/internal/engines/vm"
	"github.com/AtomiCloud/obs/internal/seed"
	"os"
	"path/filepath"
	"strings"
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
func TestReviewThreeMoreEngineForms(t *testing.T) {
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
	cases := []struct {
		name, inner string
		keeps       bool
	}{
		{"group_left_imported_extra", `checkout_charges_total{outcome="error"} / on (outcome) group_left (payment_provider) label_set(checkout_charges_total{outcome="error"}, "payment_provider", "review")`, true},
		{"count_values_own_label", `count_values by (outcome) ("payment_provider", checkout_charges_total)`, true},
		{"label_replace_inside_topk", `topk by (outcome) (1, label_replace(checkout_charges_total, "payment_provider", "$1", "payment.provider", "(.*)"))`, true},
		{"group_left_unimported_rhs_label", `checkout_charges_total{outcome="error"} / on (outcome) group_left (review_extra) label_set(checkout_charges_total{outcome="error"}, "payment_provider", "review", "review_extra", "joined")`, false},
	}
	// The engine must be closed before dottedLive opens another embedded store.
	for _, tc := range cases {
		rows, err := e.Instant(tc.inner, ds.End)
		if err != nil || len(rows) == 0 {
			t.Fatalf("%s engine rows=%+v err=%v", tc.name, rows, err)
		}
		found := false
		for _, row := range rows {
			for _, l := range row.Labels {
				if l.Name == "payment_provider" {
					found = true
				}
			}
		}
		if found != tc.keeps {
			t.Errorf("%s engine payment_provider present=%v want=%v rows=%+v", tc.name, found, tc.keeps, rows)
		}
		t.Logf("%s real engine labels: %+v", tc.name, rows)
	}
	e.Close()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			expr := `sum by (payment_provider) (` + tc.inner + `)`
			r := dottedLive(t, expr)
			t.Logf("expr=%s failures=%d findings=%+v", expr, r.Failures(), r.Findings)
			if tc.keeps {
				if r.Failures() != 0 {
					t.Errorf("engine-kept payment_provider falsely rejected")
				}
				return
			}
			n := 0
			for _, f := range r.Findings {
				if f.Rule == "label-spelling" && strings.Contains(f.Msg, `write "payment.provider"`) {
					n++
				}
			}
			if n != 2 {
				t.Errorf("engine dropped payment_provider; want alert + panel label-spelling naming payment.provider, got %d", n)
			}
		})
	}
}

// The live label scope must agree with the real engine on which side of a binary operation a query-made label
// comes from: for each form, the engine output either carries payment_provider or it does not, and the live
// check passes the outer grouping exactly when it does (review 120, jaxon).
func TestLiveBinaryScopeAgreesWithTheEngine(t *testing.T) {
	p := filepath.Join(t.TempDir(), "seed.yaml")
	if err := os.WriteFile(p, []byte(dottedSeed), 0o600); err != nil {
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
	const made = `label_set(checkout_charges_total{outcome="error"}, "payment_provider", "review")`
	const plain = `checkout_charges_total{outcome="error"}`
	cases := []struct{ name, inner string }{
		{"one_to_one_made_left", made + ` / on (outcome) ` + plain},
		{"one_to_one_made_right", plain + ` / on (outcome) ` + made},
		{"ignoring_made_left", made + ` + ignoring (payment_provider) ` + plain},
		{"ignoring_made_right", plain + ` + ignoring (payment_provider) ` + made},
		{"group_right_made_right", plain + ` / on (outcome) group_right ` + made},
		{"group_right_made_left_listed", made + ` / on (outcome) group_right (payment_provider) ` + plain},
		{"group_right_made_left_unlisted", made + ` / on (outcome) group_right (zz) ` + plain},
		{"group_left_made_left", made + ` / on (outcome) group_left ` + plain},
		// `or` also returns the right side's series that no left series matches: which ones depends on the data, so
		// the check keeps the right side's made labels too (the lenient side) and `or_made_right` is not asserted here.
		{"or_made_left", made + ` or on (outcome) ` + plain},
		{"and_made_right", plain + ` and on (outcome) ` + made},
		{"and_made_left", made + ` and on (outcome) ` + plain},
		// a scalar-like side leaves the other side's labels whole (the engine's scalar fast path)
		{"scalar_left", "2 * " + made},
		{"scalar_right", made + " * 2"},
		{"scalar_expr", "(2 - 1) * " + made},
		{"scalar_fn", "scalar(2) * " + made},
		{"vector_fn", "vector(2) * " + made},
		{"reducer_no_labels_left", "sum(" + plain + ") * " + made},
		{"scalar_with_on", made + " * on () group_left () vector(2)"},
		// group_left / group_right set each listed label from the other side: a listed label it lacks is removed
		{"group_left_listed_missing_rhs", made + " / on (outcome) group_left (payment_provider) " + plain},
		{"group_right_listed_missing_lhs", plain + " / on (outcome) group_right (payment_provider) " + made},
		{"group_left_listed_present_rhs", plain + " / on (outcome) group_left (payment_provider) " + made},
		{"comparison_on_resets", made + " >= bool on (outcome) " + plain},
		{"comparison_ignoring_resets", made + " >= ignoring (payment_provider) " + plain},
		{"unless_made_left", made + ` unless on (outcome) checkout_charges_total{outcome="x"}`},
	}
	keeps := map[string]bool{}
	for _, tc := range cases {
		rows, err := e.Instant(tc.inner, ds.End)
		if err != nil || len(rows) == 0 {
			t.Fatalf("%s: engine rows=%+v err=%v", tc.name, rows, err)
		}
		for _, row := range rows {
			for _, l := range row.Labels {
				if l.Name == "payment_provider" {
					keeps[tc.name] = true
				}
			}
		}
	}
	e.Close() // dottedLive opens another embedded store
	for _, tc := range cases {
		r := dottedLive(t, `sum by (payment_provider) (`+tc.inner+`)`)
		if keeps[tc.name] && r.Failures() != 0 {
			t.Errorf("%s: the engine keeps payment_provider, so the check must not reject it: %+v", tc.name, r.Findings)
		}
		if !keeps[tc.name] && r.Failures() != 2 {
			t.Errorf("%s: the engine drops payment_provider, so alert and panel must fail: %+v", tc.name, r.Findings)
		}
		t.Logf("%s: engine keeps=%v failures=%d", tc.name, keeps[tc.name], r.Failures())
	}
}
func TestIndependentBinaryEngineReview(t *testing.T) {
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
	const plain = `checkout_charges_total{outcome="error"}`
	const made = `label_set(checkout_charges_total{outcome="error"}, "payment_provider", "review")`
	cases := []struct {
		name, inner string
		want        bool
	}{
		{"spot_one_to_one_rhs", plain + ` / on(outcome) ` + made, false},
		{"spot_group_right_listed", made + ` / on(outcome) group_right(payment_provider) ` + plain, true},
		{"spot_and_left", made + ` and on(outcome) ` + plain, true},
		{"scalar_left_literal", `2 * ` + made, true},
		{"scalar_left_expression", `(2-1) * ` + made, true},
		{"scalar_left_function", `scalar(2) * ` + made, true},
		{"vector_empty_left", `vector(2) * ` + made, true},
		{"group_left_listed_missing_rhs", made + ` / on(outcome) group_left(payment_provider) ` + plain, false},
		{"group_right_listed_missing_lhs", plain + ` / on(outcome) group_right(payment_provider) ` + made, false},
		{"or_unmatched_rhs", `checkout_charges_total{outcome="absent"} or ` + made, true},
	}
	for _, tc := range cases {
		expr := `sum by(payment_provider) (` + tc.inner + `)`
		rows, err := e.Instant(expr, ds.End)
		if err != nil || len(rows) == 0 {
			t.Errorf("%s engine rows=%+v err=%v", tc.name, rows, err)
			continue
		}
		found := false
		for _, r := range rows {
			for _, l := range r.Labels {
				if l.Name == "payment_provider" && l.Value == "review" {
					found = true
				}
			}
		}
		if found != tc.want {
			t.Errorf("%s engine keeps=%v want=%v", tc.name, found, tc.want)
		}
		t.Logf("%s engine keeps=%v rows=%+v expr=%s", tc.name, found, rows, expr)
	}
	e.Close()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			expr := `sum by(payment_provider) (` + tc.inner + `)`
			r := dottedLive(t, expr)
			t.Logf("expr=%s failures=%d findings=%+v", expr, r.Failures(), r.Findings)
			if tc.want {
				if r.Failures() != 0 {
					t.Errorf("valid engine-kept label falsely rejected")
				}
				return
			}
			count := 0
			for _, f := range r.Findings {
				if f.Rule == "label-spelling" && strings.Contains(f.Msg, `write "payment.provider"`) {
					count++
				}
			}
			if count != 2 {
				t.Errorf("dropped-label false green: want alert and panel naming payment.provider, got %d", count)
			}
		})
	}
}
