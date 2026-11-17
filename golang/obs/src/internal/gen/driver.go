package gen

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/AtomiCloud/obs/internal/aggregation"
	"github.com/AtomiCloud/obs/internal/contract"
)

// Output is one file the run writes: Path is slash-separated, relative to the repo root
// (the folder that holds observability/).
type Output struct {
	Path    string
	Content []byte
	Lang    string // "" for the contract files (events.json, the aggregation file)
}

// Names of the contract files, relative to the observability folder.
const (
	EventsFile      = "contract/events.json"
	AggregationFile = "contract/aggregation.yaml"
)

// EventField is the log field that carries the event name (concept 3.3).
const EventField = "durian.cloud/event"

// Root is the repo root for an observability folder: the folder that holds it.
func Root(obsDir string) string {
	abs, err := filepath.Abs(obsDir)
	if err != nil {
		abs = obsDir
	}
	return filepath.Dir(abs)
}

// Plan returns every file `obs gen` writes for a valid contract: the contract files and one
// set per `generate:` target (all of them when langs is empty, else only those languages).
// The result is sorted by path; two targets that write the same path are an error.
func Plan(c *contract.Contract, obsDir string, langs []string) ([]Output, error) {
	root := Root(obsDir)
	abs, _ := filepath.Abs(obsDir)
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return nil, err
	}
	rel = filepath.ToSlash(rel)

	var outs []Output
	ev, err := Events(c)
	if err != nil {
		return nil, err
	}
	outs = append(outs,
		Output{Path: path.Join(rel, EventsFile), Content: ev},
		Output{Path: path.Join(rel, AggregationFile), Content: aggregation.Generate(c, Header(c, "# "))},
	)
	want := map[string]bool{}
	for _, l := range langs {
		want[l] = true
	}
	for _, t := range c.Generate {
		if len(want) > 0 && !want[t.Lang] {
			continue
		}
		fs, err := Generate(t.Lang, c, Options{Package: t.Package, Namespace: t.Module, Interfaces: t.Interfaces})
		if err != nil {
			return nil, err
		}
		for _, f := range fs {
			outs = append(outs, Output{Path: path.Join(filepath.ToSlash(t.Out), f.Path), Content: f.Content, Lang: t.Lang})
		}
	}
	sort.Slice(outs, func(i, j int) bool { return outs[i].Path < outs[j].Path })
	for i := 1; i < len(outs); i++ {
		if outs[i].Path == outs[i-1].Path {
			return nil, fmt.Errorf("two outputs write %s", outs[i].Path)
		}
	}
	return outs, nil
}

// Events is contract/events.json: the event names the log-alert check reads (concept 3.4, 11.1).
func Events(c *contract.Contract) ([]byte, error) {
	type ev struct {
		Event string `json:"event"`
		Level string `json:"level"`
	}
	doc := struct {
		Generated    string `json:"generated"`
		ContractHash string `json:"contract_hash"`
		Service      string `json:"service"`
		EventField   string `json:"event_field"`
		Events       []ev   `json:"events"`
	}{Generated: Marker, ContractHash: c.Hash, Service: c.Service, EventField: EventField, Events: []ev{}}
	for _, k := range c.LogOrder {
		e := c.Logs[k]
		doc.Events = append(doc.Events, ev{e.Event, e.Level})
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// LoadEvents reads the event names out of an events.json.
func LoadEvents(b []byte) ([]string, error) {
	var doc struct {
		Events []struct {
			Event string `json:"event"`
		} `json:"events"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	var out []string
	for _, e := range doc.Events {
		out = append(out, e.Event)
	}
	return out, nil
}

// Write writes every output under root.
func Write(root string, outs []Output) error {
	for _, o := range outs {
		p := filepath.Join(root, filepath.FromSlash(o.Path))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, o.Content, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// Check regenerates into a temp dir and diffs it with the files under root. It returns one
// problem per file that is missing, hand-edited, or stale (the header's contract hash is not
// the contract's). Empty means every committed file equals a fresh generate.
// Optional langs restricts orphan detection too, including removed targets whose
// language no longer appears in the current contract's target list.
func Check(root string, outs []Output, c *contract.Contract, langs ...string) ([]string, error) {
	tmp, err := os.MkdirTemp("", "obs-gen-check-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	if err := Write(tmp, outs); err != nil {
		return nil, err
	}
	var probs []string
	planned := make(map[string]bool, len(outs))
	for _, o := range outs {
		planned[filepath.Clean(filepath.FromSlash(o.Path))] = true
		fresh, err := os.ReadFile(filepath.Join(tmp, filepath.FromSlash(o.Path)))
		if err != nil {
			return nil, err
		}
		have, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(o.Path)))
		if err != nil {
			probs = append(probs, Problem(o.Path, "[gen-missing] file does not exist; run obs gen"))
			continue
		}
		if bytes.Equal(have, fresh) {
			continue
		}
		h := HashOf(have)
		if h == "" {
			h = eventsHash(have)
		}
		switch {
		case h != c.Hash:
			probs = append(probs, Problem(o.Path, "[gen-stale] header contract hash %s is not the contract's %s: the contract changed and the file was not regenerated; run obs gen (%s)",
				orNoHash(h), ShortHash(c.Hash), firstDiff(have, fresh)))
		default:
			probs = append(probs, Problem(o.Path, "[gen-edited] the file differs from a fresh generate with the same contract hash %s: it was edited by hand; run obs gen (%s)",
				ShortHash(c.Hash), firstDiff(have, fresh)))
		}
	}
	// Scan for obs-owned files too: a removed target is absent from both the plan and
	// the current target list. Looking only in current targets misses its old directory.
	// Do not follow symlinks, dependency trees, git internals, or build caches.
	selected := map[string]bool{}
	for _, lang := range langs {
		selected[lang] = true
	}
	all := len(langs) == 0
	contractPath := ContractPath(c)
	extensions := map[string]string{".go": "go", ".cs": "dotnet", ".dart": "dart", ".ts": "bun"}
	err = filepath.WalkDir(root, func(p string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if p != root {
				switch entry.Name() {
				case ".git", ".direnv", "node_modules", "vendor", ".dart_tool", ".obs-cache":
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if planned[rel] {
			return nil
		}
		if !all {
			lang := extensions[filepath.Ext(p)]
			if !selected[lang] {
				return nil
			}
		}
		file, err := os.Open(p)
		if err != nil {
			return err
		}
		head, err := io.ReadAll(io.LimitReader(file, 600))
		closeErr := file.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		line := strings.SplitN(string(head), "\n", 2)[0]
		if (line == "// "+Marker || line == "# "+Marker) && ContractOf(head) == contractPath {
			probs = append(probs, Problem(filepath.ToSlash(rel), "[gen-orphan] generated file is not in the current output plan; remove it or restore its target"))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(probs)
	return probs, nil
}

func orNoHash(h string) string {
	if h == "" {
		return "(none)"
	}
	return ShortHash(h)
}

func eventsHash(b []byte) string {
	var doc struct {
		H string `json:"contract_hash"`
	}
	if json.Unmarshal(b, &doc) == nil {
		return doc.H
	}
	return ""
}

// firstDiff names the first differing line of two files.
func firstDiff(have, fresh []byte) string {
	a, b := strings.Split(string(have), "\n"), strings.Split(string(fresh), "\n")
	for i := 0; i < len(a) || i < len(b); i++ {
		var x, y string
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x != y {
			return fmt.Sprintf("line %d: have %q, want %q", i+1, clip(x), clip(y))
		}
	}
	return "files differ"
}

func clip(s string) string {
	if len(s) > 90 {
		return s[:90] + "..."
	}
	return s
}
