package files

import (
	"fmt"
	"time"

	"github.com/AtomiCloud/obs/internal/gtime"
)

// ParseFor judges a rule's `for` (and keepFiringFor) value. It is the one place that does, so a
// value is accepted or refused the same way in a flat tier file, in `obs alerts`, and in the
// sandbox's window and forecast: Grafana's duration units (ms s m h d w y), not negative, and
// not empty (callers that allow an unset `for` check that before they call).
func ParseFor(v string) (time.Duration, error) {
	d, err := gtime.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%q is not a duration such as 0m, 10m, 2h or 1d", v)
	}
	if d < 0 {
		return 0, fmt.Errorf("%q is negative", v)
	}
	return d, nil
}
