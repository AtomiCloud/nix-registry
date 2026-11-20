// Package obsyaml reads <observability>/obs.yaml: the store and tool versions a repo
// targets and the query thresholds obs runs under. One loader serves `obs check`,
// `obs query`, `obs alerts`, `obs sandbox up` and `obs look`, so no command reads the file
// its own way.
//
// The file is small and strict: an unknown key, a wrong type or a bad value is an input
// error (exit 2) that names the file, the line and the key. A missing file is not an
// error: Load returns nil and every consumer keeps the versions and defaults built into
// this obs.
package obsyaml

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/AtomiCloud/obs/internal/engines/tempo"
	"github.com/AtomiCloud/obs/internal/engines/vl"
	"github.com/AtomiCloud/obs/internal/engines/vm"
	"github.com/AtomiCloud/obs/internal/pins"
	"github.com/AtomiCloud/obs/internal/profile"
	"gopkg.in/yaml.v3"
)

// FileName is the name of the file inside the observability folder.
const FileName = "obs.yaml"

// DashboardSchema is the dashboard schemaVersion of the Grafana this obs embeds.
// FROM SOURCE, Grafana v13.3.0: public/app/features/dashboard/state/DashboardMigrator.ts
// (DASHBOARD_SCHEMA_VERSION = 42) and apps/dashboard/pkg/migration/schemaversion/v42.go as
// the newest migration. TestDashboardSchemaMatchesGrafanaPin fails when the Grafana pin
// moves, so this number is read again on every Grafana bump.
const DashboardSchema = 42

// grafanaForDashboardSchema is the Grafana version DashboardSchema was read from.
const grafanaForDashboardSchema = "13.3.0"

// File is a parsed obs.yaml.
type File struct {
	Path     string   // the file that was read
	Versions Versions // the versions the repo targets
	// Resolved thresholds. A nil pointer or zero value means obs.yaml does not name that
	// group, and the consumer keeps its built-in default.
	Logs      *vl.Limits         // thresholds.logs over the pinned VictoriaLogs defaults
	Frontend  *tempo.Frontend    // thresholds.traces over Tempo's defaults
	MaxPoints int                // thresholds.metrics.max_points_per_series
	Profiles  profile.Tolerances // thresholds.profiles

	// sections are the top-level blocks another command owns and validates (obs live:
	// targets, estimates). They are kept as YAML nodes, with their line numbers.
	sections map[string]*yaml.Node
}

// Versions are the versions in obs.yaml; an empty string / nil means the key is absent.
type Versions struct {
	Grafana         string `yaml:"grafana"`
	VictoriaMetrics string `yaml:"victoriametrics"`
	VictoriaLogs    string `yaml:"victorialogs"`
	Tempo           string `yaml:"tempo"`
	Alloy           string `yaml:"alloy"`
	Pyroscope       string `yaml:"pyroscope"`
	DashboardSchema *int   `yaml:"dashboard_schema"`
}

type logsRaw struct {
	MaxQueryLen           *string `yaml:"max_query_len"`
	MaxQueryTimeRange     *string `yaml:"max_query_time_range"`
	MaxQueryDuration      *string `yaml:"max_query_duration"`
	MaxQueueDuration      *string `yaml:"max_queue_duration"`
	MaxConcurrentRequests *int    `yaml:"max_concurrent_requests"`
}

type tracesRaw struct {
	QueryEndCutoff      *string `yaml:"query_end_cutoff"`
	MetricsMaxDuration  *string `yaml:"metrics_max_duration"`
	MetricsMaxIntervals *int    `yaml:"metrics_max_intervals"`
}

type rawFile struct {
	Versions   Versions `yaml:"versions"`
	Thresholds struct {
		Logs    logsRaw `yaml:"logs"`
		Metrics struct {
			MaxPointsPerSeries *int `yaml:"max_points_per_series"`
		} `yaml:"metrics"`
		Traces   tracesRaw `yaml:"traces"`
		Profiles struct {
			ToleranceFunction    *float64 `yaml:"tolerance_function"`
			ToleranceSpan        *float64 `yaml:"tolerance_span"`
			ToleranceTotal       *float64 `yaml:"tolerance_total"`
			ToleranceDotnetTrace *float64 `yaml:"tolerance_dotnet_trace"`
		} `yaml:"profiles"`
	} `yaml:"thresholds"`
}

// schema lists every key; a nil value is a leaf.
var schema = map[string]any{
	// platform and service name the rule uid of a flat alert tier file: <platform>-<service>-<slug>-<severity>.
	"platform": nil, "service": nil,
	"versions": map[string]any{
		"grafana": nil, "victoriametrics": nil, "victorialogs": nil, "tempo": nil,
		"alloy": nil, "pyroscope": nil, "dashboard_schema": nil,
	},
	"thresholds": map[string]any{
		"logs": map[string]any{
			"max_query_len": nil, "max_query_time_range": nil, "max_query_duration": nil,
			"max_queue_duration": nil, "max_concurrent_requests": nil,
		},
		"metrics": map[string]any{"max_points_per_series": nil},
		"traces":  map[string]any{"query_end_cutoff": nil, "metrics_max_duration": nil, "metrics_max_intervals": nil},
		"profiles": map[string]any{
			"tolerance_function": nil, "tolerance_span": nil, "tolerance_total": nil, "tolerance_dotnet_trace": nil,
		},
	},
}

var versionRe = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+$`)
var tokenRe = regexp.MustCompile(`^[A-Za-z0-9.]+$`)

// Load reads <dir>/obs.yaml. A missing file returns (nil, nil). Any other problem is an
// error that names the file, the line and the key; callers exit 2 on it.
func Load(dir string) (*File, error) {
	path := filepath.Join(dir, FileName)
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return Parse(path, b)
}

// Parse parses the bytes of an obs.yaml read from path.
func Parse(path string, b []byte) (*File, error) {
	fail := func(line int, format string, args ...any) error {
		if line > 0 {
			return fmt.Errorf("%s:%d: %s", path, line, fmt.Sprintf(format, args...))
		}
		return fmt.Errorf("%s: %s", path, fmt.Sprintf(format, args...))
	}
	f := &File{Path: path}
	var doc yaml.Node
	dec := yaml.NewDecoder(bytes.NewReader(b))
	if err := dec.Decode(&doc); err != nil {
		if err == io.EOF { // an empty file names nothing
			return f, nil
		}
		return nil, fail(0, "%v", err)
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, fail(0, "want exactly one YAML document")
	}
	root := doc.Content[0]
	if isNull(root) { // only ~ or null: names nothing, like an empty file
		return f, nil
	}
	if root.Kind != yaml.MappingNode {
		return nil, fail(root.Line, "want a mapping with the keys versions and thresholds")
	}
	f.sections = takeSections(root)
	if err := checkKeys(root, schema, "", fail); err != nil {
		return nil, err
	}
	var raw rawFile
	if err := root.Decode(&raw); err != nil {
		var te *yaml.TypeError
		if errors.As(err, &te) && len(te.Errors) > 0 {
			return nil, typeError(te, root, fail)
		}
		return nil, fail(0, "%v", err)
	}
	f.Versions = raw.Versions
	for _, v := range []struct{ key, val string }{
		{"versions.grafana", f.Versions.Grafana}, {"versions.victoriametrics", f.Versions.VictoriaMetrics},
		{"versions.victorialogs", f.Versions.VictoriaLogs}, {"versions.tempo", f.Versions.Tempo},
		{"versions.alloy", f.Versions.Alloy}, {"versions.pyroscope", f.Versions.Pyroscope},
	} {
		if v.val != "" && !versionRe.MatchString(v.val) {
			return nil, fail(keyLine(root, v.key), "%s: %q is not a version such as 1.2.3 or v1.2.3", v.key, v.val)
		}
	}
	if s := f.Versions.DashboardSchema; s != nil && *s < 1 {
		return nil, fail(keyLine(root, "versions.dashboard_schema"), "versions.dashboard_schema: %d is not a schemaVersion (want 1 or more)", *s)
	}
	t := raw.Thresholds
	if err := f.resolveLogs(&t.Logs, root, fail); err != nil {
		return nil, err
	}
	if m := t.Metrics.MaxPointsPerSeries; m != nil {
		if *m < 1 {
			return nil, fail(keyLine(root, "thresholds.metrics.max_points_per_series"), "thresholds.metrics.max_points_per_series: %d must be at least 1", *m)
		}
		f.MaxPoints = *m
	}
	if err := f.resolveTraces(&t.Traces, root, fail); err != nil {
		return nil, err
	}
	for _, p := range []struct {
		key string
		val *float64
		dst *float64
	}{
		{"thresholds.profiles.tolerance_function", t.Profiles.ToleranceFunction, &f.Profiles.Function},
		{"thresholds.profiles.tolerance_span", t.Profiles.ToleranceSpan, &f.Profiles.Span},
		{"thresholds.profiles.tolerance_total", t.Profiles.ToleranceTotal, &f.Profiles.Total},
		{"thresholds.profiles.tolerance_dotnet_trace", t.Profiles.ToleranceDotnetTrace, &f.Profiles.DotnetTrace},
	} {
		if p.val == nil {
			continue
		}
		if math.IsNaN(*p.val) || math.IsInf(*p.val, 0) {
			return nil, fail(keyLine(root, p.key), "%s: %s is not a finite number (want a fraction between 0 and 10; 0.10 means 10 %%)", p.key, keyValue(root, p.key))
		}
		if *p.val <= 0 || *p.val > 10 {
			return nil, fail(keyLine(root, p.key), "%s: %v is not a fraction between 0 and 10 (0.10 means 10 %%)", p.key, *p.val)
		}
		*p.dst = *p.val
	}
	return f, nil
}

func (f *File) resolveLogs(l *logsRaw, root *yaml.Node, fail func(int, string, ...any) error) error {
	limits := vl.DefaultLimits()
	named := false
	apply := func(key, flag, value string) error {
		named = true
		if !tokenRe.MatchString(value) {
			return fail(keyLine(root, key), "%s: %q is not a plain value such as 16KiB, 30s or 8", key, value)
		}
		next, err := vl.ParseLimitsOver(limits, []byte(fmt.Sprintf("- \"-%s=%s\"\n", flag, value)))
		if err != nil {
			return fail(keyLine(root, key), "%s: %v", key, err)
		}
		limits = next
		return nil
	}
	for _, s := range []struct {
		key, flag string
		val       *string
	}{
		{"thresholds.logs.max_query_len", "search.maxQueryLen", l.MaxQueryLen},
		{"thresholds.logs.max_query_time_range", "search.maxQueryTimeRange", l.MaxQueryTimeRange},
		{"thresholds.logs.max_query_duration", "search.maxQueryDuration", l.MaxQueryDuration},
		{"thresholds.logs.max_queue_duration", "search.maxQueueDuration", l.MaxQueueDuration},
	} {
		if s.val == nil {
			continue
		}
		if err := apply(s.key, s.flag, *s.val); err != nil {
			return err
		}
	}
	if l.MaxConcurrentRequests != nil {
		if err := apply("thresholds.logs.max_concurrent_requests", "search.maxConcurrentRequests", fmt.Sprint(*l.MaxConcurrentRequests)); err != nil {
			return err
		}
	}
	if named {
		f.Logs = &limits
	}
	return nil
}

func (f *File) resolveTraces(t *tracesRaw, root *yaml.Node, fail func(int, string, ...any) error) error {
	fe := tempo.DefaultFrontend
	named := false
	dur := func(key string, v *string, dst *time.Duration) error {
		if v == nil {
			return nil
		}
		named = true
		d, err := time.ParseDuration(*v)
		if err != nil || d < 0 {
			return fail(keyLine(root, key), "%s: %q is not a duration such as 30s or 24h", key, *v)
		}
		*dst = d
		return nil
	}
	if err := dur("thresholds.traces.query_end_cutoff", t.QueryEndCutoff, &fe.QueryEndCutoff); err != nil {
		return err
	}
	if err := dur("thresholds.traces.metrics_max_duration", t.MetricsMaxDuration, &fe.MaxDuration); err != nil {
		return err
	}
	if t.MetricsMaxIntervals != nil {
		named = true
		if *t.MetricsMaxIntervals < 0 {
			return fail(keyLine(root, "thresholds.traces.metrics_max_intervals"), "thresholds.traces.metrics_max_intervals: %d is negative", *t.MetricsMaxIntervals)
		}
		fe.MaxIntervals = uint64(*t.MetricsMaxIntervals)
	}
	if named {
		f.Frontend = &fe
	}
	return nil
}

// checkKeys fails on the first key that is not in schema, naming its dotted path and line.
func checkKeys(n *yaml.Node, sch map[string]any, prefix string, fail func(int, string, ...any) error) error {
	if isNull(n) {
		return nil // a section with every child commented out is empty: absent
	}
	if n.Kind != yaml.MappingNode {
		return fail(n.Line, "%s: want a mapping", strings.TrimSuffix(prefix, "."))
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		sub, ok := sch[k.Value]
		if !ok {
			var known []string
			for name := range sch {
				known = append(known, name)
			}
			sort.Strings(known)
			return fail(k.Line, "unknown key %s%s (known here: %s)", prefix, k.Value, strings.Join(known, ", "))
		}
		if m, isMap := sub.(map[string]any); isMap {
			if err := checkKeys(v, m, prefix+k.Value+".", fail); err != nil {
				return err
			}
		} else if v.Kind != yaml.ScalarNode {
			return fail(v.Line, "%s%s: want one value", prefix, k.Value)
		}
	}
	return nil
}

// ownedSections are the top-level keys `obs live` reads and validates itself (targets: where
// the live stores are, estimates: expected log and trace volume). Every other command only
// has to let them pass.
var ownedSections = []string{"targets", "estimates"}

// takeSections removes the owned sections from root and returns them by key.
func takeSections(root *yaml.Node) map[string]*yaml.Node {
	out := map[string]*yaml.Node{}
	kept := root.Content[:0:0]
	for i := 0; i+1 < len(root.Content); i += 2 {
		k, v := root.Content[i], root.Content[i+1]
		owned := false
		for _, s := range ownedSections {
			if k.Value == s {
				out[s], owned = v, true
			}
		}
		if !owned {
			kept = append(kept, k, v)
		}
	}
	root.Content = kept
	return out
}

// Section is a top-level block that obs live owns (targets, estimates), or nil when the file
// does not write it. A nil File has none. The caller validates it and names the line.
func (f *File) Section(name string) *yaml.Node {
	if f == nil {
		return nil
	}
	return f.sections[name]
}

// isNull tells an empty YAML value (nothing after the colon, ~ or null).
func isNull(n *yaml.Node) bool { return n.Kind == yaml.ScalarNode && n.Tag == "!!null" }

// keyValue is the text written after a dotted key (".nan"), or "".
func keyValue(root *yaml.Node, dotted string) string {
	n := root
	for _, part := range strings.Split(dotted, ".") {
		if n == nil || n.Kind != yaml.MappingNode {
			return ""
		}
		var next *yaml.Node
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == part {
				next = n.Content[i+1]
			}
		}
		n = next
	}
	if n == nil {
		return ""
	}
	return n.Value
}

// keyLine is the line of a dotted key, or 0.
func keyLine(root *yaml.Node, dotted string) int {
	n := root
	line := 0
	for _, part := range strings.Split(dotted, ".") {
		if n == nil || n.Kind != yaml.MappingNode {
			return line
		}
		var next *yaml.Node
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == part {
				line, next = n.Content[i].Line, n.Content[i+1]
			}
		}
		n = next
	}
	return line
}

var typeErrRe = regexp.MustCompile(`^line ([0-9]+): (.*)$`)

// typeError turns the first wrong-type error of the yaml library into one that names the
// file, the line and the dotted key ("versions.dashboard_schema: cannot unmarshal ...").
func typeError(te *yaml.TypeError, root *yaml.Node, fail func(int, string, ...any) error) error {
	m := typeErrRe.FindStringSubmatch(te.Errors[0])
	if m == nil {
		return fail(0, "%s", te.Errors[0])
	}
	line, _ := strconv.Atoi(m[1])
	if key := keyAtLine(root, "", line); key != "" {
		return fail(line, "%s: %s", key, m[2])
	}
	return fail(line, "%s", m[2])
}

// keyAtLine is the dotted path of the key written on line, or "".
func keyAtLine(n *yaml.Node, prefix string, line int) string {
	if n.Kind != yaml.MappingNode {
		return ""
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		if k.Line == line {
			return prefix + k.Value
		}
		if got := keyAtLine(v, prefix+k.Value+".", line); got != "" {
			return got
		}
	}
	return ""
}

// Applied names the threshold groups obs.yaml sets.
func (f *File) Applied() []string {
	if f == nil {
		return nil
	}
	var out []string
	if f.Logs != nil {
		out = append(out, "logs")
	}
	if f.MaxPoints != 0 {
		out = append(out, "metrics")
	}
	if f.Frontend != nil {
		out = append(out, "traces")
	}
	if f.Profiles != (profile.Tolerances{}) {
		out = append(out, "profiles")
	}
	return out
}

// LoadFile reads one obs.yaml at path (obs profile compare --obs-yaml).
func LoadFile(path string) (*File, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(path, b)
}

// VLLimits are the VictoriaLogs query limits for this run: the pinned defaults, then
// obs.yaml thresholds.logs, then the settings file (--vl-settings, else
// <dir>/victorialogs-settings.yaml) key by key. A nil File uses vl.LoadLimits unchanged.
func (f *File) VLLimits(dir, settingsPath string) (vl.Limits, string, error) {
	if f == nil || f.Logs == nil {
		return vl.LoadLimits(dir, settingsPath)
	}
	return vl.LoadLimitsOver(*f.Logs, "obs.yaml thresholds.logs over VictoriaLogs v1.54.0 query defaults", dir, settingsPath)
}

// TempoFrontend is the Tempo query-frontend setting for trace rules: Tempo's defaults,
// then obs.yaml thresholds.traces, then the --tempo-config file key by key. It returns nil
// when neither names anything (the engine then uses Tempo's defaults).
func (f *File) TempoFrontend(configPath string) (*tempo.Frontend, error) {
	var fe *tempo.Frontend
	if f != nil && f.Frontend != nil {
		c := *f.Frontend
		fe = &c
	}
	if configPath == "" {
		return fe, nil
	}
	b, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("--tempo-config: %w", err)
	}
	base := tempo.DefaultFrontend
	if fe != nil {
		base = *fe
	}
	got, err := tempo.ParseFrontendOver(base, b)
	if err != nil {
		return nil, fmt.Errorf("--tempo-config %s: %w", configPath, err)
	}
	return &got, nil
}

// MetricsMaxPoints is thresholds.metrics.max_points_per_series, or the built-in default.
func (f *File) MetricsMaxPoints() int {
	if f == nil || f.MaxPoints == 0 {
		return vm.DefaultMaxPointsPerSeries
	}
	return f.MaxPoints
}

// Evaluator is one thing this obs embeds and the obs.yaml key that must agree with it.
type Evaluator struct {
	Role     string // what it evaluates
	Product  string // the pinned product
	Key      string // the obs.yaml key that names its version
	Embedded string // the version built into this binary
}

// Embedded lists the evaluators built into this obs, in print order.
func Embedded() []Evaluator {
	p := pins.All()
	return []Evaluator{
		{"MetricsQL", "VictoriaMetrics", "versions.victoriametrics", p["VictoriaMetrics"].Version},
		{"LogsQL", "VictoriaLogs", "versions.victorialogs", p["VictoriaLogs"].Version},
		{"TraceQL", "Tempo", "versions.tempo", p["Tempo"].Version},
		{"Grafana alert engine", "Grafana", "versions.grafana", p["Grafana"].Version},
		{"dashboard schema", "Grafana", "versions.dashboard_schema", fmt.Sprint(DashboardSchema)},
	}
}

// Mismatch is an obs.yaml version the embedded evaluators do not match.
type Mismatch struct {
	Key, Want, Embedded, Role string
}

func (m Mismatch) String() string {
	return fmt.Sprintf("%s: obs.yaml wants %s, this obs embeds %s (%s)", m.Key, m.Want, m.Embedded, m.Role)
}

// Mismatches lists every evaluator version obs.yaml names that this obs does not embed.
// Alloy and Pyroscope have no embedded evaluator; Notes covers them.
func (f *File) Mismatches() []Mismatch { return f.MismatchesWith(pins.All()) }

// MismatchesWith is Mismatches against a given pins table (a test seam). Besides the
// four evaluators it fails a Pyroscope version that has no known image: obs runs Pyroscope
// only as a sandbox image, and the sandbox refuses a version whose digest pins.json does
// not record, so naming one is a failure here as well, not a note.
func (f *File) MismatchesWith(known map[string]pins.Pin) []Mismatch {
	if f == nil {
		return nil
	}
	want := map[string]string{
		"versions.victoriametrics": f.Versions.VictoriaMetrics,
		"versions.victorialogs":    f.Versions.VictoriaLogs,
		"versions.tempo":           f.Versions.Tempo,
		"versions.grafana":         f.Versions.Grafana,
	}
	if f.Versions.DashboardSchema != nil {
		want["versions.dashboard_schema"] = fmt.Sprint(*f.Versions.DashboardSchema)
	}
	var out []Mismatch
	for _, e := range Embedded() {
		w := want[e.Key]
		if w == "" {
			continue
		}
		same := pins.SameVersion(w, e.Embedded)
		if !same {
			out = append(out, Mismatch{Key: e.Key, Want: w, Embedded: e.Embedded, Role: e.Role + " of " + e.Product})
		}
	}
	if w := f.Versions.Pyroscope; w != "" {
		py := known["Pyroscope"]
		have := false
		var names []string
		for _, im := range py.Images() {
			names = append(names, im.Version)
			have = have || pins.SameVersion(im.Version, w)
		}
		if !have {
			out = append(out, Mismatch{Key: "versions.pyroscope", Want: w, Embedded: py.Version,
				Role: "sandbox image of Pyroscope: no known image digest, known: " + strings.Join(names, ", ")})
		}
	}
	return out
}

// Notes are the differences that do not fail a check: an Alloy version other than the pin
// (obs embeds no evaluator for it and runs no Alloy image). A Pyroscope version with no
// known image is a mismatch, not a note.
func (f *File) Notes() []string {
	if f == nil {
		return nil
	}
	p := pins.All()
	var out []string
	if w := f.Versions.Alloy; w != "" && !pins.SameVersion(w, p["Alloy"].Version) {
		out = append(out, fmt.Sprintf("versions.alloy: obs.yaml names %s, the pin of this obs is %s (no evaluator is embedded for it and obs runs no Alloy image)", w, p["Alloy"].Version))
	}
	return out
}

// PrintEmbedded prints the evaluator versions this binary embeds.
func PrintEmbedded(w io.Writer) {
	p := pins.All()
	fmt.Fprintln(w, "evaluators embedded in this obs:")
	for _, e := range Embedded() {
		switch e.Role {
		case "dashboard schema":
			fmt.Fprintf(w, "  %-21s %s %s schemaVersion %s\n", e.Role, e.Product, p[e.Product].Version, e.Embedded)
		default:
			fmt.Fprintf(w, "  %-21s %s %s\n", e.Role, e.Product, e.Embedded)
		}
	}
	fmt.Fprintf(w, "  %-21s Alloy %s, Pyroscope %s (pins; no evaluator)\n", "reference pins", p["Alloy"].Version, p["Pyroscope"].Version)
}

// Gate is what every command that reads obs.yaml does first: it prints a FAIL line per
// mismatched version and returns their number. cmd is the command name for the lines.
func (f *File) Gate(w io.Writer, cmd string) int {
	ms := f.Mismatches()
	for _, m := range ms {
		fmt.Fprintf(w, "FAIL %s %s\n", f.Path, m)
	}
	if len(ms) > 0 {
		fmt.Fprintf(w, "%s: FAILED: %d version(s) in %s differ from the evaluators embedded in this obs (`obs check` lists them)\n", cmd, len(ms), f.Path)
	}
	return len(ms)
}
