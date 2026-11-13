package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every planted bad definition must be refused with exactly the code it names.
func TestPlantedLintFiles(t *testing.T) {
	files, _ := filepath.Glob("testdata/bad/*.yaml")
	if len(files) < 35 {
		t.Fatalf("expected the planted files, found %d", len(files))
	}
	covered := map[string]bool{}
	for _, f := range files {
		src, _ := os.ReadFile(f)
		want := strings.TrimSpace(strings.TrimPrefix(strings.SplitN(string(src), "\n", 2)[0], "# expect:"))
		_, ds := LoadModule(f)
		got := map[string]bool{}
		for _, d := range ds {
			got[d.Code] = true
		}
		if !got[want] {
			t.Errorf("%s: want %s, got %v", f, want, ds)
		}
		covered[want] = true
	}
	for code := range LintRules {
		if code == "L241" {
			continue // size budget: TestRecordBudget
		}
		if !covered[code] {
			t.Errorf("lint rule %s has no planted file", code)
		}
	}
}

// Every diff pair must be refused with the code in its directory name; ok-* must pass.
func TestDiffPairs(t *testing.T) {
	dirs, _ := filepath.Glob("testdata/diff/*")
	covered := map[string]bool{}
	for _, d := range dirs {
		st, _ := os.Stat(d)
		if !st.IsDir() {
			continue
		}
		name := filepath.Base(d)
		old, ds := LoadModule(filepath.Join(d, "old.yaml"))
		if len(ds) > 0 {
			t.Fatalf("%s old: %v", name, ds)
		}
		cur, ds := LoadModule(filepath.Join(d, "new.yaml"))
		if len(ds) > 0 {
			t.Fatalf("%s new does not lint (the case must fail in diff, not lint): %v", name, ds)
		}
		r := Diff(old, cur)
		if strings.HasPrefix(name, "ok-") {
			if len(r.Breaking) > 0 {
				t.Errorf("%s: want no breaking changes, got %v", name, r.Breaking)
			}
			continue
		}
		want := strings.SplitN(name, "-", 2)[0]
		found := false
		for _, b := range r.Breaking {
			if b.Code == want {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: want %s, got %v", name, want, r.Breaking)
		}
		covered[want] = true
	}
	for code := range DiffRules {
		if !covered[code] {
			t.Errorf("diff rule %s has no planted pair", code)
		}
	}
}

// A module whose record exceeds the etcd-derived budget is refused (L241).
func TestRecordBudget(t *testing.T) {
	var b strings.Builder
	b.WriteString("apiVersion: problems.atomi.cloud/v1\nkind: ProblemSet\nmodule: m\nversion: v1\nproblems:\n")
	desc := strings.Repeat("d", 1000)
	for i := 0; i < 1000; i++ {
		b.WriteString("  p" + strings.Repeat("x", 0) + itoa(i) + ":\n    title: T\n    status: 400\n    recoverable: true\n    detail: d\n    data:\n")
		for j := 0; j < 2; j++ {
			b.WriteString("      f" + itoa(j) + ": { type: string, description: " + desc + " }\n")
		}
	}
	f := filepath.Join(t.TempDir(), "big.yaml")
	os.WriteFile(f, []byte(b.String()), 0o644)
	_, ds := LoadModule(f)
	found := false
	for _, d := range ds {
		if d.Code == "L241" {
			found = true
		}
	}
	if !found {
		t.Fatalf("want L241, got %v", ds)
	}
}

func itoa(i int) string {
	s := ""
	if i == 0 {
		return "0"
	}
	for i > 0 {
		s = string(rune('0'+i%10)) + s
		i /= 10
	}
	return s
}
