package files

import (
	"fmt"
	"time"

	"github.com/prometheus/common/model"
)

// ParseRuleDuration is the duration reader of every rule the engines run (obs alerts, the sandbox): a
// value Prometheus's model.ParseDuration accepts (1d12h, 1y) or one Go's time.ParseDuration accepts
// (1.5m, 90s). The second is what 0.5 accepted, so no CR that passed 0.5 fails now; a flat tier file is
// held to ParseFor alone, which is the stricter standard, so whatever it passes this reads the same.
func ParseRuleDuration(v string) (time.Duration, error) {
	if d, err := model.ParseDuration(v); err == nil {
		return time.Duration(d), nil
	}
	return time.ParseDuration(v)
}

// ParseFor judges a rule's `for` (and keepFiringFor) value. It is the one place that does, so a
// value is accepted or refused the same way in a flat tier file, in `obs alerts`, and in the
// sandbox's window and forecast. It is exactly Prometheus's model.ParseDuration, which Grafana reads
// these fields with: units y w d h m s ms, biggest first (1d12h), whole numbers, "0" without a unit,
// nothing negative, no 1M. An empty value is refused (callers that allow an unset `for` check that
// before they call).
func ParseFor(v string) (time.Duration, error) {
	d, err := model.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%q is not a duration such as 0m, 10m, 2h or 1d12h (units y w d h m s ms, biggest first)", v)
	}
	return time.Duration(d), nil
}
