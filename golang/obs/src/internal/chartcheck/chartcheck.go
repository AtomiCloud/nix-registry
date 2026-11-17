// Package chartcheck compares a Helm chart folder with the engine pins
// (internal/pins/pins.json). It reads two files and never touches the network.
//
// values.yaml: every image of an engine, in one of these shapes:
//
//   - a map with `repository` and `tag` and/or `digest` (the repository names the engine);
//   - one of the engine's own `image` maps, with no repository beside it, inside the values of a
//     Chart.yaml dependency that is an engine chart (keyed by the dependency's alias, else its
//     name), at a path listed in ownImage: `victoria-logs-single.server.image.tag`. Any other image
//     map with no repository (`grafana.sidecar.image.tag`) is not the engine and is never compared;
//   - one `repo[:tag][@digest]` string under an `image` key.
//
// Chart.yaml: which engine charts the folder depends on, and `appVersion` when the chart itself
// is an engine chart. A dependency's `version` is a chart version, not an application version,
// so it is never compared with a pin.
//
// An engine the chart uses but whose version cannot be read is a failure: obs cannot prove the
// version, and a silent pass would hide the drift. Proof is per dependency (each alias of one
// engine needs its own) and per image (each engine image map or string must itself give a tag or
// a digest), so one matched occurrence never hides another unresolved one. A component whose image
// the values do not set at all falls back to the chart default and is not checked.
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

// Kinds of finding.
const (
	KindVersion    = ""            // a tag or appVersion differs from the pinned version
	KindDigest     = "digest"      // a digest differs from the pinned digest
	KindDigestOnly = "digest-only" // digest only, and obs records no digest for the engine
	KindMissing    = "missing"     // the engine is in the chart but no version or digest was found
)

// Finding is one failure: a chart value that differs from its pin, or one that cannot be proven.
type Finding struct {
	File, Key, Engine, ChartValue, PinValue, Kind string
	Line                                          int
}

func (f Finding) String() string {
	switch f.Kind {
	case KindDigest:
		return fmt.Sprintf("%s:%d %s: chart digest %s, pin %s digest %s [engine-pin-drift]", f.File, f.Line, f.Key, f.ChartValue, f.Engine, f.PinValue)
	case KindDigestOnly:
		return fmt.Sprintf("%s:%d %s: %s image %s pinned by digest only; cannot prove version %s [engine-pin-digest]", f.File, f.Line, f.Key, f.Engine, f.ChartValue, f.PinValue)
	case KindMissing:
		return fmt.Sprintf("%s:%d %s: %s is in the chart but no image tag, digest or appVersion was found (the chart default is not checked); cannot prove version %s [engine-pin-missing]", f.File, f.Line, f.Key, f.Engine, f.PinValue)
	}
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

type place struct {
	file, key string
	line      int
}

type dep struct {
	engine, name string // the engine and the chart name
	place
}

type scan struct {
	pin      map[string]pins.Pin
	res      *Result
	present  map[string]place // engine -> where an engine chart or image outside any dependency was first seen
	compared map[string]bool  // engine -> one of those had a version or digest compared, or a finding raised
	deps     map[string]dep   // values.yaml top-level key (alias or name) -> engine dependency
	proved   map[string]bool  // dependency key -> one of its images was resolved or reported
}

// ownImage lists, per engine chart, the paths (below the dependency's values key) of the engine's
// own `image` maps. Everything else with an image map (sidecars, renderers, init containers) is
// not the engine.
var ownImage = map[string][]string{
	"grafana":                    {"image"},
	"tempo":                      {"image", "tempo.image"},
	"tempo-distributed":          {"image", "tempo.image"},
	"pyroscope":                  {"image", "pyroscope.image"},
	"victoria-metrics-single":    {"server.image"},
	"victoria-metrics-cluster":   {"vmstorage.image", "vmselect.image", "vminsert.image"},
	"victoria-metrics-agent":     {"image"},
	"victoria-metrics-alert":     {"server.image"},
	"victoria-metrics-k8s-stack": {"vmcluster.spec.vmstorage.image", "vmcluster.spec.vmselect.image", "vmcluster.spec.vminsert.image", "vmsingle.spec.image", "vmagent.spec.image", "vmalert.spec.image"},
	"victoria-logs-single":       {"server.image"},
	"victoria-logs-cluster":      {"vlstorage.image", "vlselect.image", "vlinsert.image"},
}

func isOwnImage(chart string, rel []string) bool {
	j := strings.Join(rel, ".")
	for _, p := range ownImage[chart] {
		if p == j {
			return true
		}
	}
	return false
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
	s := &scan{pin: pin, res: &res, present: map[string]place{}, compared: map[string]bool{}, deps: map[string]dep{}, proved: map[string]bool{}}
	if root, err := readNode(filepath.Join(dir, "Chart.yaml")); err != nil {
		return res, err
	} else if root != nil {
		s.chartYAML(root)
	}
	if root, err := readNode(filepath.Join(dir, "values.yaml")); err != nil {
		return res, err
	} else if root != nil {
		s.walk(root, nil, "", "")
	}
	if len(s.present) == 0 && len(s.deps) == 0 {
		return res, fmt.Errorf("%w in %s (looked at Chart.yaml dependencies and values.yaml images for %s)", ErrNoEngines, dir, engineList(pin))
	}
	for _, e := range sortedPlaces(s.present) {
		if !s.compared[e] {
			p := s.present[e]
			res.Findings = append(res.Findings, Finding{File: p.file, Line: p.line, Key: p.key, Engine: e, PinValue: pin[e].Version, Kind: KindMissing})
		}
	}
	for _, key := range sortedDeps(s.deps) {
		if d := s.deps[key]; !s.proved[key] {
			res.Findings = append(res.Findings, Finding{File: d.file, Line: d.line, Key: key, Engine: d.engine, PinValue: pin[d.engine].Version, Kind: KindMissing})
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

func (s *scan) see(engine, file, key string, line int) {
	if _, ok := s.present[engine]; !ok {
		s.present[engine] = place{file, key, line}
	}
}

func (s *scan) chartYAML(root *yaml.Node) {
	if k, name := mapGet(root, "name"); name != nil {
		if e, ok := chartEngine[name.Value]; ok && s.pin[e].Version != "" {
			s.see(e, "Chart.yaml", "appVersion", k.Line)
			if k, v := mapGet(root, "appVersion"); v != nil && v.Kind == yaml.ScalarNode && v.Value != "" {
				s.compared[e] = true
				s.compare(e, "Chart.yaml", "appVersion", v.Value, k.Line)
			}
		}
	}
	if _, deps := mapGet(root, "dependencies"); deps != nil && deps.Kind == yaml.SequenceNode {
		for _, d := range deps.Content {
			k, n := mapGet(d, "name")
			if n == nil {
				continue
			}
			e, ok := chartEngine[n.Value]
			if !ok || s.pin[e].Version == "" {
				continue
			}
			key := n.Value
			if _, alias := mapGet(d, "alias"); alias != nil && alias.Value != "" {
				key = alias.Value
			}
			s.deps[key] = dep{engine: e, name: n.Value, place: place{"Chart.yaml", key, k.Line}}
		}
	}
}

// walk looks for images. ctx is the values key of the engine dependency whose values it is
// inside ("" outside any); parent is the key of the map it is in.
func (s *scan) walk(n *yaml.Node, path []string, ctx, parent string) {
	switch n.Kind {
	case yaml.MappingNode:
		s.imageMap(n, path, ctx, parent)
		for i := 0; i+1 < len(n.Content); i += 2 {
			key, val := n.Content[i], n.Content[i+1]
			p := append(append([]string{}, path...), key.Value)
			sub := ctx
			if len(path) == 0 {
				if _, ok := s.deps[key.Value]; ok {
					sub = key.Value
				}
			}
			if key.Value == "image" && val.Kind == yaml.ScalarNode {
				s.imageString(val.Value, strings.Join(p, "."), key.Line, sub)
				continue
			}
			s.walk(val, p, sub, key.Value)
		}
	case yaml.SequenceNode:
		for i, c := range n.Content {
			s.walk(c, append(append([]string{}, path...), fmt.Sprintf("[%d]", i)), ctx, parent)
		}
	}
}

// imageMap handles a map that is an engine image: it has a repository that names an engine, or it
// is one of the engine's own `image` maps (ownImage) inside the values of its dependency. Each such
// map must itself give a tag or a digest: one resolved image never stands in for another.
func (s *scan) imageMap(n *yaml.Node, path []string, ctx, parent string) {
	rk, repo := mapGet(n, "repository")
	tk, tag := mapGet(n, "tag")
	dk, dg := mapGet(n, "digest")
	var e string
	switch {
	case repo != nil && repo.Kind == yaml.ScalarNode:
		var ok bool
		if e, ok = engineOfRepo(repo.Value); !ok {
			return // some other image
		}
	case parent == "image" && ctx != "" && isOwnImage(s.deps[ctx].name, path[1:]):
		e = s.deps[ctx].engine
	default:
		return
	}
	if s.pin[e].Version == "" {
		return
	}
	key := strings.Join(path, ".")
	line := n.Line
	for _, k := range []*yaml.Node{rk, tk, dk} {
		if k != nil && k.Line < line {
			line = k.Line
		}
	}
	s.occurrence(e, key, line, ctx)
	img := image{engine: e, file: "values.yaml"}
	if tag != nil && tag.Kind == yaml.ScalarNode && tag.Value != "" {
		img.tag, img.tagLine, img.tagKey = tag.Value, tk.Line, key+".tag"
		if t, d, ok := strings.Cut(tag.Value, "@"); ok { // tag: 1.2.3@sha256:...
			img.tag, img.digest, img.digestLine, img.digestKey = t, d, tk.Line, key+".tag"
		} else if strings.HasPrefix(tag.Value, "sha256:") { // tag: sha256:...
			img.tag, img.digest, img.digestLine, img.digestKey = "", tag.Value, tk.Line, key+".tag"
		}
	}
	if dg != nil && dg.Kind == yaml.ScalarNode && dg.Value != "" {
		img.digest, img.digestLine, img.digestKey = dg.Value, dk.Line, key+".digest"
	}
	if img.tag == "" && img.digest == "" {
		s.missing(e, key, line)
		return
	}
	s.check(img)
}

// imageString handles `image: repo[:tag][@digest]`. The tag starts after the last ':' that
// follows the last '/', so a registry port is not a tag.
func (s *scan) imageString(ref, key string, line int, ctx string) {
	ref, digest, _ := strings.Cut(strings.TrimSpace(ref), "@")
	repo, tag := ref, ""
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		repo, tag = ref[:i], ref[i+1:]
	}
	e, ok := engineOfRepo(repo)
	if !ok || s.pin[e].Version == "" {
		return
	}
	s.occurrence(e, key, line, ctx)
	if tag == "" && digest == "" {
		s.missing(e, key, line)
		return
	}
	s.check(image{engine: e, file: "values.yaml", tag: tag, tagLine: line, tagKey: key, digest: digest, digestLine: line, digestKey: key})
}

// occurrence records that an engine image was found: for its dependency (every alias needs its own
// proof) or, outside any dependency, for the engine.
func (s *scan) occurrence(engine, key string, line int, ctx string) {
	if ctx != "" {
		s.proved[ctx] = true
		return
	}
	s.see(engine, "values.yaml", key, line)
}

// missing reports an engine image with no tag and no digest.
func (s *scan) missing(engine, key string, line int) {
	s.compared[engine] = true
	s.res.Findings = append(s.res.Findings, Finding{File: "values.yaml", Line: line, Key: key, Engine: engine, PinValue: s.pin[engine].Version, Kind: KindMissing})
}

// image is one engine image as the chart writes it: a tag, a digest, or both.
type image struct {
	engine, file      string
	tag, tagKey       string
	tagLine           int
	digest, digestKey string
	digestLine        int
}

// check compares what the chart pins with what obs pins. A container runtime pulls the
// digest when there is one, so a digest is compared on its own; an image pinned by digest
// only is accepted only when obs records that engine's digest.
func (s *scan) check(img image) {
	p := s.pin[img.engine]
	if img.tag != "" {
		s.compared[img.engine] = true
		s.compare(img.engine, img.file, img.tagKey, img.tag, img.tagLine)
	}
	if img.digest == "" {
		return
	}
	s.compared[img.engine] = true
	switch {
	case p.Digest != "" && img.digest == p.Digest:
		s.res.Matches = append(s.res.Matches, fmt.Sprintf("%s:%d %s: digest %s = pin %s digest", img.file, img.digestLine, img.digestKey, img.digest, img.engine))
	case p.Digest != "":
		s.res.Findings = append(s.res.Findings, Finding{File: img.file, Line: img.digestLine, Key: img.digestKey, Engine: img.engine, ChartValue: img.digest, PinValue: p.Digest, Kind: KindDigest})
	case img.tag == "":
		s.res.Findings = append(s.res.Findings, Finding{File: img.file, Line: img.digestLine, Key: img.digestKey, Engine: img.engine, ChartValue: img.digest, PinValue: p.Version, Kind: KindDigestOnly})
	default:
		s.res.Notes = append(s.res.Notes, fmt.Sprintf("%s:%d %s: digest %s is not compared: obs records no digest for %s (the tag %s was compared)", img.file, img.digestLine, img.digestKey, img.digest, img.engine, img.tag))
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

func (s *scan) compare(engine, file, key, value string, line int) {
	want := s.pin[engine].Version
	if sameVersion(engine, value, want) {
		s.res.Matches = append(s.res.Matches, fmt.Sprintf("%s:%d %s: %s = pin %s %s", file, line, key, value, engine, want))
		return
	}
	s.res.Findings = append(s.res.Findings, Finding{File: file, Line: line, Key: key, Engine: engine, ChartValue: value, PinValue: want})
}

func sortedPlaces(m map[string]place) []string {
	var s []string
	for k := range m {
		s = append(s, k)
	}
	sort.Strings(s)
	return s
}

func sortedDeps(m map[string]dep) []string {
	var s []string
	for k := range m {
		s = append(s, k)
	}
	sort.Strings(s)
	return s
}
