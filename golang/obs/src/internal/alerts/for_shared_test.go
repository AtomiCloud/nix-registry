package alerts

import (
	"testing"

	"github.com/AtomiCloud/obs/internal/files"
)

// A rule's for is judged by files.ParseFor everywhere: a value the flat-file check accepts must compile
// in obs alerts too (1d used to pass one and fail the other), and a refused value is refused by both.
func TestForAndKeepFiringForAreJudgedLikeTheFlatFileCheck(t *testing.T) {
	for _, v := range []string{"0m", "10m", "1d", "1w", "2h30m", "90s", "soon", "-5m", "1.5d", "10"} {
		_, errFiles := files.ParseFor(v)
		for name, a := range map[string]Alert{"for": {For: v}, "keepFiringFor": {KeepFiringFor: v}} {
			a.Condition = "$A > 1"
			a.Queries = map[string]Query{"A": {Datasource: "metrics", UID: "metrics", Expr: "m"}}
			_, errAlerts := compileRule(&a)
			if (errFiles == nil) != (errAlerts == nil) {
				t.Errorf("%s %q: files.ParseFor says %v, compileRule says %v", name, v, errFiles, errAlerts)
			}
		}
	}
}
