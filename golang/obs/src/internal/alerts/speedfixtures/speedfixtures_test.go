package speedfixtures

import (
	"strconv"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AtomiCloud/obs/internal/files"
	"github.com/AtomiCloud/obs/internal/seed"
)

func TestSetsLoad(t *testing.T) {
	out := t.TempDir()
	if err := Write(out); err != nil {
		t.Fatal(err)
	}
	for _, n := range Sizes {
		dir := filepath.Join(out, "n"+itoa(n))
		s, err := files.Load(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(s.Problems) != 0 {
			t.Fatalf("n%d: %v", n, s.Problems)
		}
		if s.Alerts != n {
			t.Errorf("n%d: %d alert files", n, s.Alerts)
		}
		logs := 0
		for _, q := range s.Queries {
			if q.Kind == files.Logs {
				logs++
				if !strings.Contains(q.Expr, "| stats") {
					t.Errorf("n%d %s: log query without stats: %s", n, q.Where, q.Expr)
				}
			}
			if strings.Contains(strings.ToLower(q.Expr), "count_over_time") || q.Kind == files.Loki {
				t.Errorf("n%d %s: LogQL left in: %s", n, q.Where, q.Expr)
			}
		}
		if n >= 20 && logs == 0 {
			t.Errorf("n%d: no log alerts", n)
		}
		for _, l := range Lengths {
			sd := filepath.Join(dir, "seed."+l+".yaml")
			if _, err := seed.Load(sd); err != nil {
				t.Errorf("n%d %s: %v", n, l, err)
			}
		}
	}
}

func TestVariantsDiffer(t *testing.T) {
	seen := map[string]string{}
	for rel, body := range Alerts(1000) {
		if prev, ok := seen[body]; ok {
			t.Fatalf("%s and %s are identical", rel, prev)
		}
		seen[body] = rel
	}
}

func TestNoRealNames(t *testing.T) {
	for _, bad := range []string{"pichu", "charlotte", "primordial"} {
		if strings.Contains(strings.ToLower(Seed("4h")), bad) {
			t.Errorf("seed names %q", bad)
		}
	}
}

func itoa(n int) string { return strconv.Itoa(n) }
