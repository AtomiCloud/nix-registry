// Package chartcheck compares a Helm chart folder with the engine pins
// (internal/pins/pins.json). It reads two files and never touches the network:
//
//   - values.yaml: every image of a known engine, as a map with `repository` and `tag` or as
//     one `repo:tag` string under an `image` key;
//   - Chart.yaml: which engine charts the folder depends on, and `appVersion` when the chart
//     itself is an engine chart. A dependency's `version` is a chart version, not an
//     application version, so it is never compared with a pin.
package chartcheck

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/AtomiCloud/obs/internal/pins"
)

// Finding is one drift: a chart value that differs from its pin.
type Finding struct {
	File, Key, Engine, ChartValue, PinValue string
	Line                                    int
}

func (f Finding) String() string {
	return fmt.Sprintf("%s:%d %s: chart %s, pin %s %s [engine-pin-drift]", f.File, f.Line, f.Key, f.ChartValue, f.Engine, f.PinValue)
}

// Result is what one run read.
type Result struct {
	Findings []Finding
	Matches  []string
	Notes    []string
}

// ErrNoEngines means the folder holds no engine at all (a usage or input error).
var ErrNoEngines = errors.New("no engine found")

// imageEngine maps the last path segment of an image repository to a pin name.
var imageEngine = map[string]string{
	"victoria-metrics": "VictoriaMetrics", "vmstorage": "VictoriaMetrics", "vminsert": "VictoriaMetrics",
	"vmselect": "VictoriaMetrics", "vmagent": "VictoriaMetrics", "vmalert": "VictoriaMetrics", "vmauth": "VictoriaMetrics",
	"victoria-logs": "VictoriaLogs",
	"tempo":         "Tempo",
	"grafana":       "Grafana",
	"pyroscope":     "Pyroscope",
}

// chartEngine maps a Helm chart name to a pin name.
var chartEngine = map[string]string{
	"victoria-metrics-single": "VictoriaMetrics", "victoria-metrics-cluster": "VictoriaMetrics",
	"victoria-metrics-k8s-stack": "VictoriaMetrics", "victoria-metrics-agent": "VictoriaMetrics",
	"victoria-metrics-alert": "VictoriaMetrics",
	"victoria-logs-single":   "VictoriaLogs", "victoria-logs-cluster": "VictoriaLogs",
	"tempo": "Tempo", "tempo-distributed": "Tempo",
	"grafana":   "Grafana",
	"pyroscope": "Pyroscope",
}

// Check reads dir and compares it with pin. A missing or unreadable folder is an error;
// a folder holding no engine returns ErrNoEngines.
func Check(dir string, pin map[string]pins.Pin) (Result, error) {
	var res Result
	st, err := os.Stat(dir)
	if err != nil {
		return res, err
	}
	if !st.IsDir() {
		return res, fmt.Errorf("%s is not a folder", dir)
	}
	seen := map[string]bool{}   // engines present in the chart
	tagged := map[string]bool{} // engines whose version was compared

	if root, err := readNode(filepath.Join(dir, "Chart.yaml")); err != nil {
		return res, err
	} else if root != nil {
		chartYAML(root, pin, &res, seen, tagged)
	}
	if root, err := readNode(filepath.Join(dir, "values.yaml")); err != nil {
		return res, err
	} else if root != nil {
		walk(root, nil, "values.yaml", pin, &res, seen, tagged)
	}
	if len(seen) == 0 {
		return res, fmt.Errorf("%w in %s (looked at Chart.yaml dependencies and values.yaml images for %s)", ErrNoEngines, dir, engineList(pin))
	}
	for _, e := range sortedKeys(seen) {
		if !tagged[e] {
			res.Notes = append(res.Notes, fmt.Sprintf("%s is in the chart but no image tag or appVersion for it was found; its version is NOT compared (pin %s)", e, pin[e].Version))
		}
	}
	return res, nil
}

func engineList(pin map[string]pins.Pin) string {
	var names []string
	for _, n := range []string{"VictoriaMetrics", "VictoriaLogs", "Tempo", "Grafana", "Pyroscope"} {
		if _, ok := pin[n]; ok {
			names = append(names, n)
		}
	}
	return strings.Join(names, ", ")
}

func readNode(file string) (*yaml.Node, error) {
	b, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var n yaml.Node
	if err := yaml.Unmarshal(b, &n); err != nil {
		return nil, fmt.Errorf("%s: %v", filepath.Base(file), err)
	}
	if len(n.Content) == 0 {
		return nil, nil
	}
	return n.Content[0], nil
}

func mapGet(n *yaml.Node, key string) (*yaml.Node, *yaml.Node) {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil, nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i], n.Content[i+1]
		}
	}
	return nil, nil
}

func chartYAML(root *yaml.Node, pin map[string]pins.Pin, res *Result, seen, tagged map[string]bool) {
	if _, name := mapGet(root, "name"); name != nil {
		if e, ok := chartEngine[name.Value]; ok {
			seen[e] = true
			if k, v := mapGet(root, "appVersion"); v != nil && v.Kind == yaml.ScalarNode {
				tagged[e] = true
				compare(res, pin, e, "Chart.yaml", "appVersion", v.Value, k.Line)
			}
		}
	}
	if _, deps := mapGet(root, "dependencies"); deps != nil && deps.Kind == yaml.SequenceNode {
		for _, d := range deps.Content {
			if _, n := mapGet(d, "name"); n != nil {
				if e, ok := chartEngine[n.Value]; ok {
					seen[e] = true
				}
			}
		}
	}
}

func walk(n *yaml.Node, path []string, file string, pin map[string]pins.Pin, res *Result, seen, tagged map[string]bool) {
	switch n.Kind {
	case yaml.MappingNode:
		// An image map: repository + tag.
		if _, repo := mapGet(n, "repository"); repo != nil && repo.Kind == yaml.ScalarNode {
			if e, ok := engineOfRepo(repo.Value); ok && pin[e].Version != "" {
				seen[e] = true
				if k, tag := mapGet(n, "tag"); tag != nil && tag.Kind == yaml.ScalarNode && tag.Value != "" {
					tagged[e] = true
					compare(res, pin, e, file, strings.Join(append(append([]string{}, path...), "tag"), "."), tag.Value, k.Line)
				}
			}
		}
		for i := 0; i+1 < len(n.Content); i += 2 {
			key, val := n.Content[i], n.Content[i+1]
			p := append(append([]string{}, path...), key.Value)
			// An image string: `image: repo:tag`.
			if key.Value == "image" && val.Kind == yaml.ScalarNode {
				if repo, tag, ok := strings.Cut(strings.TrimSpace(val.Value), ":"); ok {
					if e, ok := engineOfRepo(repo); ok && pin[e].Version != "" {
						seen[e] = true
						tagged[e] = true
						compare(res, pin, e, file, strings.Join(p, "."), tag, key.Line)
					}
				}
				continue
			}
			walk(val, p, file, pin, res, seen, tagged)
		}
	case yaml.SequenceNode:
		for i, c := range n.Content {
			walk(c, append(append([]string{}, path...), fmt.Sprintf("[%d]", i)), file, pin, res, seen, tagged)
		}
	}
}

func engineOfRepo(repo string) (string, bool) {
	repo = strings.TrimSpace(repo)
	if i := strings.LastIndex(repo, "/"); i >= 0 {
		repo = repo[i+1:]
	}
	e, ok := imageEngine[repo]
	return e, ok
}

// sameVersion treats a leading v as no difference (VictoriaMetrics tags carry it, Tempo's
// and Grafana's do not) and accepts VictoriaMetrics' `-cluster` image suffix.
func sameVersion(engine, chart, pin string) bool {
	c, p := strings.TrimPrefix(chart, "v"), strings.TrimPrefix(pin, "v")
	if engine == "VictoriaMetrics" {
		c = strings.TrimSuffix(c, "-cluster")
	}
	return c == p
}

func compare(res *Result, pin map[string]pins.Pin, engine, file, key, value string, line int) {
	want := pin[engine].Version
	if sameVersion(engine, value, want) {
		res.Matches = append(res.Matches, fmt.Sprintf("%s:%d %s: %s = pin %s %s", file, line, key, value, engine, want))
		return
	}
	res.Findings = append(res.Findings, Finding{File: file, Line: line, Key: key, Engine: engine, ChartValue: value, PinValue: want})
}

func sortedKeys(m map[string]bool) []string {
	var s []string
	for k := range m {
		s = append(s, k)
	}
	sort.Strings(s)
	return s
}
