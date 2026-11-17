//go:build parity

package check

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/AtomiCloud/obs/internal/engines/vl"
)

func TestParityVictoriaLogsLimits(t *testing.T) {
	base := os.Getenv("OBS_PARITY_VL")
	if base == "" {
		t.Skip("OBS_PARITY_VL not set")
	}
	limits := vl.Limits{MaxQueryLen: 16384, MaxQueryTimeRange: 7 * 24 * time.Hour}
	end := time.Now().UTC().Truncate(time.Second)
	for _, tc := range []struct {
		name, query string
		duration    time.Duration
		refused     bool
	}{
		{"ordinary", "* | stats count() n", time.Hour, false},
		{"query-at-limit", "_msg:=" + strconv.Quote(strings.Repeat("x", limits.MaxQueryLen-8)), time.Hour, false},
		{"query-over-limit", "_msg:=" + strconv.Quote(strings.Repeat("x", limits.MaxQueryLen-7)), time.Hour, true},
		{"time-range-at-limit", "*", limits.MaxQueryTimeRange, false},
		{"time-range-over-limit", "*", limits.MaxQueryTimeRange + time.Second, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start := end.Add(-tc.duration)
			ours := limits.Check(tc.query, start, end)
			params := url.Values{"query": {tc.query}, "start": {fmt.Sprint(start.Unix())}, "end": {fmt.Sprint(end.Unix())}, "limit": {"1"}}
			resp, err := (&http.Client{Timeout: 30 * time.Second}).Get(base + "/select/logsql/query?" + params.Encode())
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			refused := resp.StatusCode/100 != 2
			if refused != tc.refused || (ours != nil) != refused {
				t.Fatalf("%s: query bytes=%d range=%s: obs guard=%v; real VictoriaLogs HTTP %d; expected refusal=%v", tc.name, len(tc.query), tc.duration, ours, resp.StatusCode, tc.refused)
			}
		})
	}
	t.Log("MEASURED deterministic maxQueryLen/maxQueryTimeRange refusals; chart settings and runtime timeout/concurrency equivalence NOT TESTED")
}
