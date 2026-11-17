//go:build parity

package check

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/AtomiCloud/obs/internal/engines/vl"
	"github.com/AtomiCloud/obs/internal/seed"
)

func TestParityVictoriaLogsLimits(t *testing.T) {
	base := os.Getenv("OBS_PARITY_VL")
	if base == "" {
		t.Skip("OBS_PARITY_VL not set")
	}
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/victorialogs-settings.yaml", []byte("-search.maxQueryLen: 16KiB\n-search.maxQueryTimeRange: 7d\n"), 0600); err != nil {
		t.Fatal(err)
	}
	limits, note, err := vl.LoadLimits(dir, "")
	if err != nil || note != "" {
		t.Fatalf("chart settings: %q %v", note, err)
	}
	engine, err := vl.NewWithLimits(&seed.Dataset{}, limits)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
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
		{"authored-time-intersection", "_time:1h", limits.MaxQueryTimeRange + time.Hour, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start := end.Add(-tc.duration)
			ours := limits.Check(tc.query, start, end)
			_, embedded := engine.Rows(tc.query, start, end)
			params := url.Values{"query": {tc.query}, "start": {fmt.Sprint(start.Unix())}, "end": {fmt.Sprint(end.Unix())}, "limit": {"1"}}
			resp, err := (&http.Client{Timeout: 30 * time.Second}).Get(base + "/select/logsql/query?" + params.Encode())
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
			if err != nil {
				t.Fatal(err)
			}
			refused := resp.StatusCode/100 != 2
			if refused != tc.refused || (ours != nil) != refused || (embedded != nil) != refused {
				t.Fatalf("%s: query bytes=%d range=%s: guard=%v embedded=%v; real VictoriaLogs HTTP %d: %s; expected refusal=%v", tc.name, len(tc.query), tc.duration, ours, embedded, resp.StatusCode, body, tc.refused)
			}
			if refused {
				flag := "-search.maxQueryLen"
				if strings.Contains(tc.name, "time-range") {
					flag = "-search.maxQueryTimeRange"
				}
				if !strings.Contains(embedded.Error(), flag) || !strings.Contains(string(body), flag) {
					t.Fatalf("refusal must name %s: obs %v; real %s", flag, embedded, body)
				}
			}
		})
	}
	t.Log("MEASURED settings-file -> embedded engine vs real VL: six length/range cases including authored time intersection; runtime timeout/concurrency equivalence NOT TESTED")
}
