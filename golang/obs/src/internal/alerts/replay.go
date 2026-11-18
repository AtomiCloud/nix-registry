package alerts

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/AtomiCloud/obs/internal/contract"
	"github.com/AtomiCloud/obs/internal/engines/tempo"
	"github.com/AtomiCloud/obs/internal/engines/vl"
	"github.com/AtomiCloud/obs/internal/engines/vm"
	"github.com/AtomiCloud/obs/internal/logevent"
	"github.com/AtomiCloud/obs/internal/obsyaml"
	"github.com/AtomiCloud/obs/internal/seed"
)

// ReplayDataset runs the existing alert evaluator/state machine on the exact
// sandbox timeline. It is a replay computed by obs, not Grafana's own history.
// Recording samples are returned too, so the sandbox can store the same inputs.
// The caller serializes this with other embedded VM evaluations, as Check does.
func ReplayDataset(dir, seedPath string, ds *seed.Dataset, window ...Run) ([]AlertRun, []*seed.Series, error) {
	p, err := loadProject(dir, seedPath)
	if err != nil {
		return nil, nil, err
	}
	// Check assumes non-event filters true on synthetic logs. Use the same
	// normalization here, so replay and obs alerts have identical fire times.
	var events logevent.Events
	cp := filepath.Join(dir, "contract.yaml")
	if _, err := os.Stat(cp); err == nil {
		c, err := contract.Load(cp)
		if err != nil {
			return nil, nil, err
		}
		events = c
	} else if !os.IsNotExist(err) {
		return nil, nil, err
	}
	for _, group := range [][]Alert{p.Alerts, p.Records} {
		for j := range group {
			for ref, q := range group[j].Queries {
				if q.Datasource != "logs" {
					continue
				}
				a, err := logevent.Analyze(q.Expr, events)
				if err != nil {
					return nil, nil, fmt.Errorf("%s rule %s query %s: %w", group[j].File, group[j].ID, ref, err)
				}
				q.Expr = a.Expr
				group[j].Queries[ref] = q
			}
		}
	}
	of, err := obsyaml.Load(dir)
	if err != nil {
		return nil, nil, err
	}
	var used []*Alert
	for j := range p.Alerts {
		used = append(used, &p.Alerts[j])
	}
	for j := range p.Records {
		used = append(used, &p.Records[j])
	}
	eng := Engines{Datasources: map[string]DatasourceEngine{}}
	if needsDS(used, "metrics") || len(p.Records) > 0 {
		m, err := vm.New(ds)
		if err != nil {
			return nil, nil, err
		}
		defer m.Close()
		m.SetMaxPoints(of.MetricsMaxPoints())
		eng.Metrics = m
		eng.Datasources["metrics"] = metricEngine{m}
	}
	if needsDS(used, "logs") {
		limits, _, err := of.VLLimits(dir, "")
		if err != nil {
			return nil, nil, err
		}
		l, err := vl.NewWithLimits(ds, limits)
		if err != nil {
			return nil, nil, err
		}
		defer l.Close()
		eng.Datasources["logs"] = logsEngine{e: l, guard: checkedLogsGuard{limits: limits}}
	}
	if needsDS(used, "traces") {
		fe, err := of.TempoFrontend("")
		if err != nil {
			return nil, nil, err
		}
		eng.Datasources["traces"] = tracesEngine{e: tempo.NewTempoEngine(ds.Traces), fe: fe}
	}
	runs := []Run{{Name: "look-replay", Start: ds.Start, End: ds.End}}
	// A caller can forecast a fixed snapshot from a fresh scheduler state without
	// generating future data. Only the run's evaluation times change.
	if len(window) > 1 {
		return nil, nil, fmt.Errorf("replay accepts at most one evaluation window")
	}
	if len(window) == 1 {
		if !window[0].End.After(window[0].Start) {
			return nil, nil, fmt.Errorf("replay evaluation window must be positive")
		}
		runs = window
	}
	cache := &queryCache{m: map[qKey]*qEntry{}}
	var recorded []*Series
	if _, err := runRecording(p, runs, eng, cache, &recorded); err != nil {
		return nil, nil, err
	}
	result, _ := evaluateAll(p, runs, eng, cache, 1, true, nil)
	for _, run := range result {
		if run.Err != "" {
			return nil, nil, fmt.Errorf("rule %s replay: %s", run.Alert, run.Err)
		}
	}
	return result, recorded, nil
}
