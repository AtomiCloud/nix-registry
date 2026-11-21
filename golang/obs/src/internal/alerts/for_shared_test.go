package alerts

import (
	"testing"
	"time"

	"github.com/AtomiCloud/obs/internal/files"
)

// A rule's for and keepFiringFor: whatever a flat tier file passes (Prometheus durations) compiles in
// obs alerts, and no CR that passed 0.5 (Go durations) fails now.
func TestForAndKeepFiringForAreReadLikeTheFlatFileCheckAndLikeZeroPointFive(t *testing.T) {
	for _, v := range []string{"0m", "10m", "1d", "1w", "2h30m", "90s", "1d12h", "1y", "0", "1.5m", "2.5h", "1us", "soon", "-5m", "1.5d", "10", "1M", "1m1h x"} {
		_, flatErr := files.ParseFor(v)
		old, oldErr := time.ParseDuration(v)
		for name, a := range map[string]Alert{"for": {For: v}, "keepFiringFor": {KeepFiringFor: v}} {
			a.Condition = "$A > 1"
			a.Queries = map[string]Query{"A": {Datasource: "metrics", UID: "metrics", Expr: "m"}}
			_, err := compileRule(&a)
			if flatErr == nil && err != nil {
				t.Errorf("%s %q: a flat file passes it but obs alerts refuses: %v", name, v, err)
			}
			if oldErr == nil && old >= 0 && err != nil {
				t.Errorf("%s %q: 0.5 accepted it, obs alerts refuses now: %v", name, v, err)
			}
		}
	}
}
