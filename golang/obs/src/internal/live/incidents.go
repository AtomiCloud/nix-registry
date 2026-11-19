package live

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/AtomiCloud/obs/internal/files"
	"github.com/AtomiCloud/obs/internal/seed"
	"gopkg.in/yaml.v3"
)

// IncidentOptions of Incidents.
type IncidentOptions struct {
	Dir     string        // the observability folder (alerts/, incidents/)
	Since   time.Duration // the default look-back window
	Windows []Window      // windows the user names
	Now     func() time.Time
}

type interval struct{ From, To time.Time }

// FiredAlert is an alert that fired on the target in the window.
type FiredAlert struct {
	ID       string        `json:"id"`
	Title    string        `json:"title"`
	Firings  int           `json:"firings"`
	First    time.Time     `json:"first"`
	Last     time.Time     `json:"last"`
	Longest  time.Duration `json:"longest_ns"`
	InRepo   bool          `json:"in_repo"`
	Runbook  string        `json:"runbook_url,omitempty"`
	Incident string        `json:"incident,omitempty"`
	// StartedBeforeWindow: the alert was already firing when the window began; First is the window start.
	StartedBeforeWindow bool                `json:"started_before_window,omitempty"`
	GrafanaAlertID      int64               `json:"grafana_alert_id,omitempty"`
	SpanUnknown         bool                `json:"span_unknown,omitempty"`
	Observations        []FiringObservation `json:"observations,omitempty"`
	ruleUID             string
	spans               []interval
}

// FiringObservation proves firing at a transition, without assuming a continuous span.
// Previous means firing immediately before At; otherwise firing starts/continues at At.
type FiringObservation struct {
	At       time.Time `json:"at"`
	Previous bool      `json:"previous,omitempty"`
}

func (o FiringObservation) inside(from, to time.Time) bool {
	if o.Previous {
		return o.At.After(from) && !o.At.After(to)
	}
	return !o.At.Before(from) && o.At.Before(to)
}

// Missed is an incident (a file, or a window the user named) that no alert caught.
type Missed struct {
	Incident   string  `json:"incident,omitempty"`
	Window     *Window `json:"window,omitempty"`
	Reason     string  `json:"reason"`
	Suggestion string  `json:"suggested_entry,omitempty"`
}

// RuleRow is one alert rule of the repo.
type RuleRow struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	File  string `json:"file"`
	Fired bool   `json:"fired"`
}

// WindowResult lists what fired inside one named window.
type WindowResult struct {
	Window  Window   `json:"window"`
	Fired   []string `json:"fired"`
	Unknown string   `json:"unknown,omitempty"`
}

// IncidentsResult of `obs live incidents`.
type IncidentsResult struct {
	Target             string         `json:"target"`
	From               time.Time      `json:"from"`
	To                 time.Time      `json:"to"`
	Source             string         `json:"source"`
	SourceWhy          string         `json:"source_why"`
	Fired              []FiredAlert   `json:"fired"`
	FiredNoIncident    []FiredAlert   `json:"fired_but_no_incident"`
	IncidentNotAlerted []Missed       `json:"incident_not_alerted"`
	NoRunbook          []RuleRow      `json:"alert_without_runbook"`
	Windows            []WindowResult `json:"windows,omitempty"`
	Suggestions        []string       `json:"suggested_incident_entries"`
	HistoryIncomplete  bool           `json:"history_incomplete,omitempty"`
	HistoryNote        string         `json:"history_note,omitempty"`
	UnknownState       []RuleRow      `json:"unknown_state,omitempty"`
}

type annotation struct {
	AlertID   int64             `json:"alertId"`
	AlertUID  string            `json:"alertUID"`
	AlertName string            `json:"alertName"`
	PrevState string            `json:"prevState"`
	NewState  string            `json:"newState"`
	Time      int64             `json:"time"`
	Labels    map[string]string `json:"labels"`
	Data      struct {
		Labels map[string]string `json:"labels"`
	} `json:"data"`
}

func alerting(s string) bool {
	s = strings.ToLower(s)
	return strings.HasPrefix(s, "alerting") || strings.HasPrefix(s, "recovering") || s == "firing"
}

// stateLookback bounds the extra history read before the window. A rule with no transition
// in that history still has unknown state; silence does not establish that it was quiet.
const stateLookback = 7 * 24 * time.Hour
const annotationLimit = 10000

type annotationCoverage struct {
	Rules       map[string]historyRule
	Known       map[string]bool // rules whose instance inventory and states are established
	CurrentNote string
	Incomplete  bool
}

type historyRule struct {
	UID, Title string
	AlertID    int64
}

func (r historyRule) key() string {
	if r.UID != "" {
		return r.UID
	}
	if r.AlertID != 0 {
		return "grafana-alert-id:" + strconv.FormatInt(r.AlertID, 10)
	}
	return "unknown-rule:" + r.Title
}

func labelIdentity(labels map[string]string) (string, bool) {
	if labels == nil {
		return "", false
	}
	b, _ := json.Marshal(labels) // encoding/json sorts map keys and escapes values losslessly.
	return string(b), true
}

func (a annotation) instanceIdentity() (string, bool) {
	if a.Labels != nil && a.Data.Labels != nil {
		x, _ := labelIdentity(a.Labels)
		y, _ := labelIdentity(a.Data.Labels)
		if x != y {
			return "", false
		}
	}
	if a.Labels != nil {
		return labelIdentity(a.Labels)
	}
	return labelIdentity(a.Data.Labels)
}

// fromAnnotations reads Grafana's alert state history (one entry per state change) from
// stateLookback before the window to its end, rebuilds when each alert was alerting, and keeps
// what overlaps from..to. An alert already firing at from has its first span start at from.
func fromAnnotations(ctx context.Context, c *Client, t *Target, from, to time.Time) (map[string]*FiredAlert, *annotationCoverage, error) {
	readFrom := from.Add(-stateLookback)
	q := url.Values{"type": {"alert"}, "from": {strconv.FormatInt(readFrom.UnixMilli(), 10)}, "to": {strconv.FormatInt(to.UnixMilli(), 10)}, "limit": {strconv.Itoa(annotationLimit)}}
	var all []annotation
	if err := c.GetJSON(ctx, t.Grafana, "/api/annotations", q, &all); err != nil {
		return nil, nil, err
	}
	coverage := &annotationCoverage{Rules: map[string]historyRule{}, Known: map[string]bool{}, Incomplete: len(all) >= annotationLimit}
	current, currentErr := currentRules(ctx, c, t)
	if currentErr != nil {
		coverage.CurrentNote = "current alert state unavailable: " + currentErr.Error()
	}
	// Resolve numeric history IDs to a current UID only through a unique title, and only
	// when history has no conflicting UID evidence. Never merge two same-title rules.
	historyTitles := map[string]map[string]bool{}
	for _, a := range all {
		r := historyRule{UID: a.AlertUID, Title: a.AlertName, AlertID: a.AlertID}
		if historyTitles[r.Title] == nil {
			historyTitles[r.Title] = map[string]bool{}
		}
		historyTitles[r.Title][r.key()] = true
	}
	currentTitles := map[string][]currentRule{}
	for _, r := range current {
		currentTitles[r.Name] = append(currentTitles[r.Name], r)
	}
	identity := func(a annotation) historyRule {
		r := historyRule{UID: a.AlertUID, Title: a.AlertName, AlertID: a.AlertID}
		if r.UID == "" && r.AlertID != 0 && len(historyTitles[r.Title]) == 1 && len(currentTitles[r.Title]) == 1 {
			r.UID = currentTitles[r.Title][0].UID
		}
		return r
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].Time < all[j].Time })
	spans := map[string][]interval{}
	observations := map[string][]FiringObservation{}
	ambiguous := map[string]bool{}
	type instance struct{ Rule, Labels string }
	open := map[instance]*interval{}
	seen := map[instance]bool{}
	for _, a := range all {
		rule := identity(a)
		key := rule.key()
		coverage.Rules[key] = rule
		at := time.UnixMilli(a.Time).UTC()
		labels, lossless := a.instanceIdentity()
		if !lossless || (rule.UID == "" && rule.AlertID == 0) {
			ambiguous[key] = true
			var obs *FiringObservation
			if alerting(a.NewState) {
				obs = &FiringObservation{At: at}
			} else if alerting(a.PrevState) {
				obs = &FiringObservation{At: at, Previous: true}
			}
			if obs != nil && obs.inside(from, to) {
				observations[key] = append(observations[key], *obs)
			}
			continue // tags and annotation text do not provide lossless instance identity
		}
		inst := instance{Rule: key, Labels: labels}
		if !seen[inst] && alerting(a.PrevState) {
			// the history starts after the alert began firing: it was already alerting when the read began
			start := readFrom
			if coverage.Incomplete {
				start = at // missing earlier transitions cannot establish firing back to readFrom
			}
			open[inst] = &interval{From: start}
		}
		seen[inst] = true
		switch {
		case alerting(a.NewState) && open[inst] == nil:
			open[inst] = &interval{From: at}
		case !alerting(a.NewState) && open[inst] != nil:
			iv := open[inst]
			iv.To = at
			spans[key] = append(spans[key], *iv)
			open[inst] = nil
		}
	}
	for _, r := range current {
		key := r.UID
		if _, ok := coverage.Rules[key]; !ok {
			coverage.Rules[key] = historyRule{UID: r.UID, Title: r.Name}
		}
		// The snapshot may be read after the annotation cutoff. Its last evaluation
		// must fall inside the history we read, or a just-resolved alert could look
		// inactive for an earlier window in which it was actually firing.
		if r.Health != "ok" || r.IsPaused || r.LastEvaluation.IsZero() || r.LastEvaluation.After(to.Truncate(time.Millisecond)) {
			coverage.Known[key] = false
			continue
		}
		known := currentErr == nil && !coverage.Incomplete && !ambiguous[key]
		if r.State != "inactive" && r.State != "pending" && !alerting(r.State) {
			known = false
		}
		if alerting(r.State) && len(r.Alerts) == 0 {
			known = false
		}
		currentSeen := map[string]bool{}
		for _, a := range r.Alerts {
			labels, ok := labelIdentity(a.Labels)
			if !ok || currentSeen[labels] {
				known = false
				continue
			}
			currentSeen[labels] = true
			inst := instance{Rule: key, Labels: labels}
			if !knownCurrentState(a.State) {
				known = false
				continue
			}
			if !seen[inst] && !ambiguous[key] && !coverage.Incomplete {
				if alerting(a.State) {
					if a.ActiveAt == nil || a.ActiveAt.After(readFrom) {
						known = false
						continue
					}
					open[inst] = &interval{From: readFrom}
				}
			} else if seen[inst] && (open[inst] != nil) != alerting(a.State) {
				known = false
			}
		}
		coverage.Known[key] = known
	}
	for inst, iv := range open {
		if iv != nil {
			iv.To = to
			spans[inst.Rule] = append(spans[inst.Rule], *iv)
		}
	}
	out := map[string]*FiredAlert{}
	for key, ss := range spans {
		for _, iv := range mergeIntervals(ss) {
			if !iv.To.After(from) || !iv.From.Before(to) {
				continue // over before the window began, or after it ended
			}
			f := out[key]
			if f == nil {
				r := coverage.Rules[key]
				f = &FiredAlert{ID: key, Title: r.Title, GrafanaAlertID: r.AlertID, ruleUID: r.UID, SpanUnknown: ambiguous[key]}
				out[key] = f
			}
			if iv.From.Before(from) {
				f.StartedBeforeWindow = true
				iv.From = from
			}
			if iv.To.After(to) {
				iv.To = to
			}
			f.Firings++
			if f.First.IsZero() || iv.From.Before(f.First) {
				f.First = iv.From
			}
			f.Last = iv.From
			f.spans = append(f.spans, iv)
		}
	}
	for key, obs := range observations {
		f := out[key]
		if f == nil {
			r := coverage.Rules[key]
			f = &FiredAlert{ID: key, Title: r.Title, GrafanaAlertID: r.AlertID, ruleUID: r.UID}
			out[key] = f
		}
		f.SpanUnknown = true
		f.Observations = obs
		for _, o := range obs {
			if f.First.IsZero() || o.At.Before(f.First) {
				f.First = o.At
			}
			if o.At.After(f.Last) {
				f.Last = o.At
			}
		}
	}
	return out, coverage, nil
}

func knownCurrentState(s string) bool {
	return alerting(s) || strings.EqualFold(s, "normal") || strings.EqualFold(s, "inactive") || strings.EqualFold(s, "pending")
}

// mergeIntervals unions the firing spans of all instances of one rule, in time order.
func mergeIntervals(spans []interval) []interval {
	sort.Slice(spans, func(i, j int) bool { return spans[i].From.Before(spans[j].From) })
	var merged []interval
	for _, span := range spans {
		if len(merged) == 0 || span.From.After(merged[len(merged)-1].To) {
			merged = append(merged, span)
		} else if span.To.After(merged[len(merged)-1].To) {
			merged[len(merged)-1].To = span.To
		}
	}
	return merged
}

// fromAlertsSeries reads the ALERTS{alertstate="firing"} series a vmalert-style ruler writes.
func fromAlertsSeries(ctx context.Context, c *Client, t *Target, from, to time.Time) (map[string]*FiredAlert, error) {
	step := time.Minute
	if need := time.Duration(float64(to.Sub(from)) / 10000); need > step {
		step = need.Round(time.Second) + time.Second
	}
	mats, err := c.Range(ctx, t.Metrics, `max by (alertname, grafana_rule_uid) (ALERTS{alertstate="firing"})`, from, to, step)
	if err != nil {
		return nil, err
	}
	out := map[string]*FiredAlert{}
	for _, m := range mats {
		key := m.Labels["grafana_rule_uid"]
		if key == "" {
			key = m.Labels["alertname"]
		}
		f := out[key]
		if f == nil {
			f = &FiredAlert{ID: key, Title: m.Labels["alertname"], ruleUID: m.Labels["grafana_rule_uid"]}
			out[key] = f
		}
		var cur *interval
		var prev int64
		for _, s := range m.Samples {
			if cur == nil || s.T-prev > int64(2*step.Seconds()) {
				if cur != nil {
					f.spans = append(f.spans, *cur)
				}
				cur = &interval{From: time.Unix(s.T, 0).UTC()}
				f.Firings++
				if f.First.IsZero() || cur.From.Before(f.First) {
					f.First = cur.From
				}
				f.Last = cur.From
			}
			cur.To = time.Unix(s.T, 0).UTC().Add(step)
			prev = s.T
		}
		if cur != nil {
			f.spans = append(f.spans, *cur)
		}
	}
	return out, nil
}

type incidentFile struct {
	Name string
	Inc  seed.Incident
}

func readIncidents(dir string) ([]incidentFile, error) {
	paths, _ := filepath.Glob(filepath.Join(dir, "incidents", "*.yaml"))
	sort.Strings(paths)
	var out []incidentFile
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		var i seed.Incident
		if err := yaml.Unmarshal(b, &i); err != nil {
			return nil, fmt.Errorf("%s: %v", p, err)
		}
		name := strings.TrimSuffix(filepath.Base(p), ".yaml")
		i.Name = name
		out = append(out, incidentFile{Name: name, Inc: i})
	}
	return out, nil
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func slug(s string) string {
	return strings.Trim(slugRe.ReplaceAllString(strings.ToLower(s), "-"), "-")
}

func entry(story, id string, dur time.Duration) string {
	if dur < 10*time.Minute {
		dur = 10 * time.Minute
	}
	d := DurText(dur.Round(time.Minute))
	fire := "[]"
	if id != "" {
		fire = "[" + id + "]"
	}
	return fmt.Sprintf("story: %s\nwindow: { start: 10m, duration: %s } # offsets inside seed.length: where the seed places it\nchange: [] # what changed in the data: use `obs live shape`; each entry is { metric, where, rate | latency_x | share_x }\nexpect: { fire: %s, quiet: [], fire_within: 5m, resolve_within: 5m }", strconv.Quote(story), d, fire)
}

// Incidents reads the alerts that fired on the target and compares them with the repo.
func Incidents(ctx context.Context, c *Client, t *Target, o IncidentOptions) (*IncidentsResult, error) {
	if o.Now == nil {
		o.Now = time.Now
	}
	now := o.Now().UTC()
	from := now.Add(-o.Since)
	for _, w := range o.Windows {
		if w.From.Before(from) {
			from = w.From
		}
	}
	res := &IncidentsResult{Target: t.Name, From: from, To: now}
	if t.Grafana == "" && t.Metrics == "" {
		return nil, fmt.Errorf("target %q has no grafana URL and no metrics URL: obs live incidents reads Grafana's alert state history, or the ALERTS series (set targets.%s.grafana)", t.Name, t.Name)
	}
	const grafanaWhy = "Grafana-managed alert rules keep every state change in Grafana's state history; they do not write an ALERTS series (only a vmalert-style ruler does)"
	var fired map[string]*FiredAlert
	var err error
	var coverage *annotationCoverage
	if t.Grafana != "" {
		fired, coverage, err = fromAnnotations(ctx, c, t, from, now)
		if err == nil {
			res.Source, res.SourceWhy = "Grafana state history (/api/annotations?type=alert)", grafanaWhy
		} else if t.Metrics == "" {
			return nil, fmt.Errorf("alert state history: %w", err)
		} else {
			res.SourceWhy = "Grafana's state history could not be read (" + err.Error() + "), so the ALERTS series is used"
		}
	} else {
		res.SourceWhy = "the target has no grafana URL, so the ALERTS series is used (it exists only when a vmalert-style ruler wrote it)"
	}
	if res.Source == "" {
		fired, err = fromAlertsSeries(ctx, c, t, from, now)
		if err != nil {
			return nil, fmt.Errorf("alerts series: %w", err)
		}
		res.Source = "ALERTS series in VictoriaMetrics (ALERTS{alertstate=\"firing\"})"
	}

	set, err := files.Load(o.Dir)
	if err != nil {
		return nil, err
	}
	type rule struct {
		ID, Title, File, Runbook string
		Record                   bool
	}
	var rules []rule
	for _, r := range set.Rules {
		id := r.Definition.UID
		if id == "" {
			id = r.Name
		}
		rules = append(rules, rule{ID: id, Title: r.Definition.Title, File: r.File, Runbook: r.Definition.Annotations["runbook_url"], Record: r.Definition.Record != nil})
	}
	incs, err := readIncidents(o.Dir)
	if err != nil {
		return nil, err
	}
	covered := map[string]string{} // rule id -> the incident that expects it to fire
	ruleIDs := map[string]bool{}
	byID := map[string]rule{}
	byTitle := map[string][]rule{}
	for _, r := range rules {
		ruleIDs[r.ID] = true
		byID[r.ID] = r
		if r.Title != "" {
			byTitle[r.Title] = append(byTitle[r.Title], r)
		}
	}
	matchedHistory := map[string]string{}
	knownRepo := map[string]bool{}
	canMiss := true
	if coverage != nil {
		titleCount := map[string]int{}
		for _, r := range coverage.Rules {
			titleCount[r.Title]++
		}
		canMiss = !coverage.Incomplete && coverage.CurrentNote == ""
		for key, h := range coverage.Rules {
			id := ""
			if h.UID != "" {
				if _, ok := byID[h.UID]; ok {
					id = h.UID
				}
			} else if h.AlertID != 0 && titleCount[h.Title] == 1 && len(byTitle[h.Title]) == 1 {
				id = byTitle[h.Title][0].ID
			}
			if id != "" {
				matchedHistory[key] = id
				knownRepo[id] = coverage.Known[key]
			}
			if !coverage.Known[key] {
				canMiss = false
			}
		}
		for _, r := range rules {
			if !r.Record && !knownRepo[r.ID] {
				res.UnknownState = append(res.UnknownState, RuleRow{ID: r.ID, Title: r.Title, File: r.File})
				canMiss = false
			}
		}
		res.HistoryIncomplete = coverage.Incomplete
		res.HistoryNote = "current Grafana rules and instance states are combined with uncapped history; missing or ambiguous identities leave state unknown; tag-only transitions prove firing observations, not spans"
		if coverage.CurrentNote != "" {
			res.HistoryNote += "; " + coverage.CurrentNote
		}
		if coverage.Incomplete {
			res.HistoryNote += fmt.Sprintf("; incomplete history: the response reached the %d-entry limit, so named windows cannot be declared missed", annotationLimit)
		}
		sort.Slice(res.UnknownState, func(i, j int) bool { return res.UnknownState[i].ID < res.UnknownState[j].ID })
	}
	for _, i := range incs {
		for _, id := range i.Inc.Expect.Fire {
			if _, ok := covered[id]; !ok {
				covered[id] = i.Name
			}
		}
	}

	// Match explicit UIDs first. A conflicting UID never falls back to a title; a title
	// without UID evidence must be unique in both the source history and the repo.
	var ids []string
	for k := range fired {
		ids = append(ids, k)
	}
	sort.Strings(ids)
	firedRule := map[string]bool{}
	for _, k := range ids {
		f := fired[k]
		match := matchedHistory[k]
		if coverage == nil {
			if _, ok := byID[f.ID]; ok {
				match = f.ID
			} else if f.ruleUID == "" && len(byTitle[f.Title]) == 1 {
				match = byTitle[f.Title][0].ID
			}
		}
		if r, ok := byID[match]; ok {
			f.ID, f.InRepo, f.Runbook = r.ID, true, r.Runbook
			if f.Title == "" {
				f.Title = r.Title
			}
			firedRule[r.ID] = true
		}
		for _, s := range f.spans {
			if d := s.To.Sub(s.From); d > f.Longest {
				f.Longest = d
			}
		}
		f.Incident = covered[f.ID]
		res.Fired = append(res.Fired, *f)
	}
	sort.SliceStable(res.Fired, func(i, j int) bool { return res.Fired[i].ID < res.Fired[j].ID })
	for _, f := range res.Fired {
		if f.Incident == "" {
			res.FiredNoIncident = append(res.FiredNoIncident, f)
			title := f.Title
			if title == "" {
				title = f.ID
			}
			story := fmt.Sprintf("%s fired %d time(s) on %s between %s and %s", title, f.Firings, t.Name, f.First.Format(time.RFC3339), f.Last.Format(time.RFC3339))
			if f.SpanUnknown {
				story = fmt.Sprintf("firing observed for %s on %s between %s and %s; spans, duration and firing count unknown", title, t.Name, f.First.Format(time.RFC3339), f.Last.Format(time.RFC3339))
			}
			res.Suggestions = append(res.Suggestions, fmt.Sprintf("# suggested incidents/%s.yaml\n%s", slug(f.ID), entry(story, f.ID, f.Longest)))
		}
	}

	// incident files that no alert is expected to catch
	for _, i := range incs {
		live := 0
		for _, id := range i.Inc.Expect.Fire {
			if ruleIDs[id] {
				live++
			}
		}
		switch {
		case len(i.Inc.Expect.Fire) == 0:
			res.IncidentNotAlerted = append(res.IncidentNotAlerted, Missed{Incident: i.Name, Reason: "incidents/" + i.Name + ".yaml has no alert under expect.fire, so no alert catches it (obs test: incident not caught by any alert)"})
		case live == 0:
			res.IncidentNotAlerted = append(res.IncidentNotAlerted, Missed{Incident: i.Name, Reason: "incidents/" + i.Name + ".yaml names alerts under expect.fire that are not rules in alerts/: " + strings.Join(i.Inc.Expect.Fire, ", ")})
		}
	}
	// windows the user names
	for _, w := range o.Windows {
		wr := WindowResult{Window: w, Fired: []string{}}
		for _, f := range res.Fired {
			caught := false
			for _, s := range f.spans {
				if s.From.Before(w.To) && s.To.After(w.From) {
					caught = true
					break
				}
			}
			for _, o := range f.Observations {
				if o.inside(w.From, w.To) {
					caught = true
					break
				}
			}
			if caught {
				wr.Fired = append(wr.Fired, f.ID)
			}
		}
		if len(wr.Fired) == 0 && coverage != nil && !canMiss {
			wr.Unknown = "unknown from Grafana history: current state or instance identity could not establish coverage"
		}
		res.Windows = append(res.Windows, wr)
		if len(wr.Fired) == 0 && wr.Unknown == "" {
			ww := w
			story := fmt.Sprintf("real incident on %s between %s and %s: no alert caught it", t.Name, w.From.Format(time.RFC3339), w.To.Format(time.RFC3339))
			sug := fmt.Sprintf("# suggested incidents/%s.yaml: fire stays empty until you add the alert that should have caught it\n%s", slug(w.From.Format("2006-01-02t1504")), entry(story, "", w.To.Sub(w.From)))
			res.IncidentNotAlerted = append(res.IncidentNotAlerted, Missed{Window: &ww, Reason: "no alert caught it: nothing fired between " + w.From.Format(time.RFC3339) + " and " + w.To.Format(time.RFC3339), Suggestion: sug})
		}
	}
	for _, r := range rules {
		if r.Runbook == "" && !r.Record {
			res.NoRunbook = append(res.NoRunbook, RuleRow{ID: r.ID, Title: r.Title, File: r.File, Fired: firedRule[r.ID]})
		}
	}
	sort.SliceStable(res.NoRunbook, func(i, j int) bool { return res.NoRunbook[i].ID < res.NoRunbook[j].ID })
	return res, nil
}

// Print writes the text for people. It writes no file.
func (r *IncidentsResult) Print(w io.Writer) {
	fmt.Fprintf(w, "obs live incidents: target %s, %s to %s (UTC)\n", r.Target, r.From.Format(time.RFC3339), r.To.Format(time.RFC3339))
	fmt.Fprintf(w, "source: %s\nwhy: %s\n", r.Source, r.SourceWhy)
	if r.HistoryNote != "" {
		fmt.Fprintf(w, "history: %s\n", r.HistoryNote)
	}
	for _, rule := range r.UnknownState {
		fmt.Fprintf(w, "  %s: state over the window is unknown from history (current state or unambiguous instance history unavailable)\n", rule.ID)
	}
	fmt.Fprintf(w, "\nFIRED (%d alert(s))\n", len(r.Fired))
	for _, f := range r.Fired {
		repo := ""
		if !f.InRepo {
			repo = " [not a rule in this repo]"
		}
		early := ""
		if f.StartedBeforeWindow {
			early = " (already firing when the window began; first is the window start)"
		}
		if f.SpanUnknown {
			fmt.Fprintf(w, "  %s: firing observed, first %s; spans, duration and firing count unknown%s\n", f.ID, f.First.Format(time.RFC3339), repo)
			continue
		}
		fmt.Fprintf(w, "  %s: %d firing(s), first %s, longest %s in the window%s%s\n", f.ID, f.Firings, f.First.Format(time.RFC3339), f.Longest.Round(time.Second), early, repo)
	}
	for _, wr := range r.Windows {
		fmt.Fprintf(w, "  window %s..%s: fired %v\n", wr.Window.From.Format(time.RFC3339), wr.Window.To.Format(time.RFC3339), wr.Fired)
		if wr.Unknown != "" {
			fmt.Fprintf(w, "    %s\n", wr.Unknown)
		}
	}
	fmt.Fprintf(w, "\nfired-but-no-incident (%d): an alert fired and no incident file expects it\n", len(r.FiredNoIncident))
	for _, f := range r.FiredNoIncident {
		fmt.Fprintf(w, "  %s\n", f.ID)
	}
	fmt.Fprintf(w, "\nincident-not-alerted (%d): an incident no alert catches\n", len(r.IncidentNotAlerted))
	for _, m := range r.IncidentNotAlerted {
		fmt.Fprintf(w, "  %s\n", m.Reason)
	}
	fmt.Fprintf(w, "\nalert-without-runbook (%d): a rule with no runbook_url annotation\n", len(r.NoRunbook))
	for _, a := range r.NoRunbook {
		fired := ""
		if a.Fired {
			fired = " (fired in this window)"
		}
		fmt.Fprintf(w, "  %s in %s%s\n", a.ID, a.File, fired)
	}
	sug := append([]string(nil), r.Suggestions...)
	for _, m := range r.IncidentNotAlerted {
		if m.Suggestion != "" {
			sug = append(sug, m.Suggestion)
		}
	}
	if len(sug) > 0 {
		fmt.Fprintln(w, "\nSUGGESTED incident.yaml ENTRIES (text only: obs live incidents writes nothing; add one in a pull request, then obs test shows what to fix)")
		for _, s := range sug {
			fmt.Fprintf(w, "\n%s\n", s)
		}
	}
	fmt.Fprintln(w, "\nobs live incidents: nothing is judged here; exit 0")
}

// JSON is the machine form.
func (r *IncidentsResult) JSON() ([]byte, error) { return json.MarshalIndent(r, "", "  ") }
