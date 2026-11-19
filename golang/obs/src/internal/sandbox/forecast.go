package sandbox

import (
	"strings"
	"time"

	"github.com/AtomiCloud/obs/internal/alerts"
	"github.com/AtomiCloud/obs/internal/files"
	"github.com/AtomiCloud/obs/internal/seed"
)

// NativeRuleWindow is a prediction for screenshots, not observed Grafana state.
// Grafana's first evaluation is scheduled within one interval after provisioning.
type NativeRuleWindow struct {
	UID           string `json:"uid"`
	Expected      string `json:"expected"`
	ProvisionedAt int64  `json:"provisioned_at_ms"`
	For           string `json:"for"`
	Interval      string `json:"interval"`
	FiringAt      int64  `json:"expected_firing_by_ms,omitempty"`
	HoldUntil     int64  `json:"snapshot_holds_until_ms,omitempty"`
	Supported     bool   `json:"snapshot_supports_expected_state"`
}

func nativeForecast(dir, seedPath string, set *files.Set, ds *seed.Dataset, provisioned time.Time, fire []string) ([]NativeRuleWindow, error) {
	// Forecast ten minutes of the existing snapshot. No generated future samples
	// are sent to any store. Beyond this bound the prompt promises no state.
	end := provisioned.Add(10 * time.Minute)
	runs, _, err := alerts.ReplayDataset(dir, seedPath, ds, alerts.Run{Name: "look-forecast", Start: provisioned, End: end})
	if err != nil {
		return nil, err
	}
	wanted := map[string]bool{}
	for _, uid := range fire {
		wanted[uid] = true
	}
	byUID := map[string]alerts.AlertRun{}
	for _, run := range runs {
		byUID[run.Alert] = run
	}
	var out []NativeRuleWindow
	for _, rr := range set.Rules {
		if rr.Definition.Record != nil {
			continue
		}
		uid := rr.Definition.UID
		if uid == "" {
			uid = rr.Name
		}
		pending, interval := time.Duration(0), time.Minute
		if rr.Definition.For != "" {
			pending, _ = time.ParseDuration(rr.Definition.For)
		}
		if rr.Interval != "" {
			interval, _ = time.ParseDuration(rr.Interval)
		}
		row := NativeRuleWindow{UID: uid, Expected: "Normal", ProvisionedAt: provisioned.UnixMilli(), For: pending.String(), Interval: interval.String()}
		if !wanted[uid] {
			row.Supported = len(byUID[uid].Steps) > 0
			for _, step := range byUID[uid].Steps {
				for _, state := range step.States {
					if !strings.HasPrefix(state, "Normal") {
						row.Supported = false
					}
				}
			}
			out = append(out, row)
			continue
		}
		row.Expected = "Firing"
		row.FiringAt = provisioned.Add(firingDeadline(pending, interval)).UnixMilli()
		began := false
		for _, step := range byUID[uid].Steps {
			if step.T < row.FiringAt {
				continue
			}
			firing := false
			for _, state := range step.States {
				firing = firing || strings.HasPrefix(state, "Alerting") || strings.HasPrefix(state, "Recovering")
			}
			if !began && !firing {
				break
			}
			if firing {
				began = true
				row.Supported = true
				row.HoldUntil = end.UnixMilli()
			} else {
				row.HoldUntil = step.T
				break
			}
		}
		out = append(out, row)
	}
	return out, nil
}

// firingDeadline is how long after provisioning a rule is sure to be Firing. Grafana evaluates every interval
// and fires at the first evaluation whose time in Pending has reached "for", so the pending period counts in
// whole intervals (for 15 s at 10 s: two), and the first evaluation may be a full interval after provisioning.
func firingDeadline(pending, interval time.Duration) time.Duration {
	if interval <= 0 {
		return pending + interval
	}
	whole := (pending + interval - 1) / interval * interval
	return whole + interval
}
