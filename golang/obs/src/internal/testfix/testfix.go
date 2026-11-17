// Package testfix finds the tests/ folder (fixtures and golden files) for Go tests. It is searched
// for in this order: $OBS_TESTS_DIR; the first ancestor of the working directory that holds
// tests/fixtures (the git layout golang/obs/tests, and the source archive's root/tests); the
// folder next to the source root (the nix build copies ${./tests} there). A build from src/
// alone has none: the callers skip, and tests/run.sh in the nix check runs the same comparisons.
package testfix

import (
	"os"
	"path/filepath"
	"testing"
)

// Dir returns the tests folder, or "" when there is none.
func Dir() string {
	if d := os.Getenv("OBS_TESTS_DIR"); d != "" {
		return d
	}
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for i := 0; i < 8; i++ {
		p := filepath.Join(dir, "tests")
		if st, err := os.Stat(filepath.Join(p, "fixtures")); err == nil && st.IsDir() {
			return p
		}
		dir = filepath.Dir(dir)
	}
	return ""
}

// Path joins elements under the tests folder; it skips the test when there is no tests folder.
func Path(t testing.TB, elem ...string) string {
	t.Helper()
	d := Dir()
	if d == "" {
		t.Skip("tests/ not found (build from src/ alone); set OBS_TESTS_DIR")
	}
	return filepath.Join(append([]string{d}, elem...)...)
}

// GoodContract is the fixture contract of the generator tests.
func GoodContract(t testing.TB) string {
	t.Helper()
	return Path(t, "fixtures", "gen", "good", "observability", "contract.yaml")
}
