package live

import (
	"context"
	"fmt"
	"time"
)

const currentRulesPath = "/api/prometheus/grafana/api/v1/rules"

type currentInstance struct {
	Labels   map[string]string `json:"labels"`
	State    string            `json:"state"`
	ActiveAt *time.Time        `json:"activeAt"`
}

type currentRule struct {
	UID      string            `json:"uid"`
	Name     string            `json:"name"`
	Type     string            `json:"type"`
	State    string            `json:"state"`
	Health   string            `json:"health"`
	IsPaused bool              `json:"isPaused"`
	Alerts   []currentInstance `json:"alerts"`
}

// currentRules reads an existing GET allowlist entry. No filters or limits: the snapshot
// must include inactive rules and every instance. Partial/failed answers establish nothing.
func currentRules(ctx context.Context, c *Client, t *Target) ([]currentRule, error) {
	var body struct {
		Status string `json:"status"`
		Data   *struct {
			Groups []struct {
				Rules []currentRule `json:"rules"`
			} `json:"groups"`
			Next string `json:"groupNextToken"`
		} `json:"data"`
		Warnings []string `json:"warnings"`
	}
	if err := c.GetJSON(ctx, t.Grafana, currentRulesPath, nil, &body); err != nil {
		return nil, err
	}
	if body.Status != "success" || body.Data == nil || body.Data.Next != "" || len(body.Warnings) > 0 {
		return nil, fmt.Errorf("current alert state is failed or incomplete")
	}
	var out []currentRule
	seen := map[string]bool{}
	for _, g := range body.Data.Groups {
		for _, r := range g.Rules {
			if r.Type == "recording" {
				continue
			}
			if r.UID == "" || seen[r.UID] {
				return nil, fmt.Errorf("current alert state has ambiguous rule identities")
			}
			seen[r.UID] = true
			out = append(out, r)
		}
	}
	return out, nil
}
