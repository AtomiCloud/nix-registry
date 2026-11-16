package vm

// Guard against silent data loss: VictoriaMetrics storage drops samples whose
// timestamps are more than 2 days ahead of the wall clock (or older than the
// retention) and only counts them. obs reads those counters after every write and
// fails instead of querying partial data.

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"

	"github.com/VictoriaMetrics/metrics"
)

var ignoredRe = regexp.MustCompile(`(?m)^vm_rows_ignored_total\{reason="(big_timestamp|small_timestamp)"\} (\d+)`)

func checkDropped() error {
	var b bytes.Buffer
	metrics.WritePrometheus(&b, false)
	d := map[string]uint64{}
	for _, m := range ignoredRe.FindAllStringSubmatch(b.String(), -1) {
		n, err := strconv.ParseUint(m[2], 10, 64)
		if err != nil {
			return err
		}
		d[m[1]] += n
	}
	if d["big_timestamp"]+d["small_timestamp"] > 0 {
		return fmt.Errorf("metric store dropped samples (too new: %d, too old: %d); the seed's time line must end in the past (check --base)", d["big_timestamp"], d["small_timestamp"])
	}
	return nil
}
