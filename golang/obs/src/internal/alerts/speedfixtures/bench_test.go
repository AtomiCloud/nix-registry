//go:build speedbench

package speedfixtures

// The §11.1.1 speed table. Not part of `go test ./...`: run it by hand (heavy lock!):
//
//	OBS_BIN=/path/to/obs OBS_FIXTURES=../../../../tests/fixtures/alerts-speed \
//	  go test -tags speedbench -run TestSpeedTable -v -timeout 1h ./internal/alerts/speedfixtures
//
// TestTraceSpeedTable runs the TraceQL sets (t1, t20) the same way.
//
// Each run is a fresh process pinned to 2 CPUs (taskset -c 0,1), with an empty result
// cache (OBS_CACHE_DIR = a new empty folder), on a copy of the set in a RAM-backed
// folder (/dev/shm). Time is wall clock of the process; peak memory is the process's
// maximum resident set (getrusage ru_maxrss). OBS_RUNS (default 3) runs per cell: the
// table shows the median time and the largest peak.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"syscall"
	"testing"
	"time"
)

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	if out, err := exec.Command("cp", "-r", src, dst).CombinedOutput(); err != nil {
		t.Fatalf("cp: %v: %s", err, out)
	}
}

func TestSpeedTable(t *testing.T) { speedTable(t, "n", Sizes) }

// TestTraceSpeedTable is the same table for the TraceQL-only sets t1 and t20.
func TestTraceSpeedTable(t *testing.T) { speedTable(t, "t", TraceSizes) }

func speedTable(t *testing.T, prefix string, sizes []int) {
	bin, fix := os.Getenv("OBS_BIN"), os.Getenv("OBS_FIXTURES")
	if bin == "" || fix == "" {
		t.Skip("set OBS_BIN and OBS_FIXTURES")
	}
	runs := 3
	if v, err := strconv.Atoi(os.Getenv("OBS_RUNS")); err == nil && v > 0 {
		runs = v
	}
	cpus := os.Getenv("OBS_CPUS")
	if cpus == "" {
		cpus = "0,1"
	}
	base := "/dev/shm"
	if _, err := os.Stat(base); err != nil {
		base = t.TempDir()
	}
	type cell struct {
		secs float64
		kb   int64
		bad  bool // a run exited non-zero: the timing is not a measurement
	}
	res := map[string]cell{}
	for _, n := range sizes {
		for _, l := range Lengths {
			var times []float64
			var peak int64
			bad := false
			for r := 0; r < runs; r++ {
				work, err := os.MkdirTemp(base, "obs-speed-")
				if err != nil {
					t.Fatal(err)
				}
				src := filepath.Join(fix, fmt.Sprintf("%s%d", prefix, n))
				dir := filepath.Join(work, "observability")
				copyTree(t, src, dir)
				if err := os.Rename(filepath.Join(dir, "seed."+l+".yaml"), filepath.Join(dir, "seed.yaml")); err != nil {
					t.Fatal(err)
				}
				cache := filepath.Join(work, "cache")
				_ = os.Mkdir(cache, 0o755)
				cmd := exec.Command("taskset", "-c", cpus, bin, "alerts", "--dir", dir)
				cmd.Env = append(os.Environ(), "OBS_CACHE_DIR="+cache, "TMPDIR="+work)
				t0 := time.Now()
				out, err := cmd.CombinedOutput()
				el := time.Since(t0).Seconds()
				if err != nil {
					bad = true
					t.Errorf("n=%d %s run %d: %v\n%s", n, l, r, err, tail(out))
				}
				ru := cmd.ProcessState.SysUsage().(*syscall.Rusage)
				if ru.Maxrss > peak {
					peak = ru.Maxrss
				}
				times = append(times, el)
				_ = os.RemoveAll(work)
			}
			sort.Float64s(times)
			res[fmt.Sprintf("%d/%s", n, l)] = cell{times[len(times)/2], peak, bad}
		}
	}
	fmt.Printf("\n| Alerts | %s seed | %s seed | Peak memory %s / %s |\n|---|---|---|---|\n", Lengths[0], Lengths[1], Lengths[0], Lengths[1])
	for _, n := range sizes {
		a, b := res[fmt.Sprintf("%d/%s", n, Lengths[0])], res[fmt.Sprintf("%d/%s", n, Lengths[1])]
		show := func(c cell) string {
			if c.bad {
				return "FAILED"
			}
			return fmt.Sprintf("%.2f s", c.secs)
		}
		fmt.Printf("| %s | %s | %s | %.2f / %.2f GiB |\n", fmtN(n), show(a), show(b), float64(a.kb)/1048576, float64(b.kb)/1048576)
	}
	fmt.Printf("\n(cpus=%s, runs per cell=%d, median time, largest peak)\n", cpus, runs)
}

func tail(b []byte) string {
	if len(b) > 600 {
		b = b[len(b)-600:]
	}
	return string(b)
}

func fmtN(n int) string {
	if n >= 1000 {
		return fmt.Sprintf("%d,%03d", n/1000, n%1000)
	}
	return strconv.Itoa(n)
}
