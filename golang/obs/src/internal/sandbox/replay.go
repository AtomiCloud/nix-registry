package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/AtomiCloud/obs/internal/alerts"
)

func replayTransitions(incident string, runs []alerts.AlertRun) []ReplayTransition {
	var out []ReplayTransition
	for _, run := range runs {
		if len(run.Steps) > 0 {
			var keys []string
			for key := range run.Steps[0].States {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				out = append(out, ReplayTransition{Incident: incident, Alert: run.Alert, At: run.Steps[0].T, To: run.Steps[0].States[key]})
			}
		}
		for _, tr := range run.Transitions {
			out = append(out, ReplayTransition{Incident: incident, Alert: run.Alert, At: tr.T, From: tr.From, To: tr.To})
		}
	}
	return out
}

func liveSnapshotExpired(info *Info, now time.Time) bool {
	if len(info.Incidents) == 0 {
		return false
	}
	if info.LiveIncident == "" {
		return true
	}
	for _, inc := range info.Incidents {
		if inc.Name == info.LiveIncident && now.UnixMilli() >= inc.To {
			return true
		}
	}
	for _, rule := range info.LiveRules {
		if rule.Expected == "Firing" && rule.Supported && now.UnixMilli() >= rule.HoldUntil {
			return true
		}
	}
	return false
}

func (c *Config) checkLookReceiver(ctx context.Context, have map[string]existing) error {
	comp, _ := ByName("tempo")
	name := containerName(c.Project, comp)
	if _, ok := have[name]; !ok {
		return nil
	}
	b, err := c.Runner.Run(ctx, "inspect", "--format", "{{json .HostConfig.PortBindings}}", name)
	if err != nil {
		return fmt.Errorf("checking Tempo receiver: %w", err)
	}
	var bindings map[string][]json.RawMessage
	if json.Unmarshal([]byte(b), &bindings) != nil || len(bindings["4318/tcp"]) == 0 {
		return fmt.Errorf("Tempo receiver is not published: run `obs sandbox down` first (stack from an older obs); use --project %s", c.Project)
	}
	return nil
}
