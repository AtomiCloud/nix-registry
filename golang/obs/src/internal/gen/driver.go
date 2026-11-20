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
	// A source: entry is somebody else's metric: the generators and the aggregation see the contract
	// without it, and only the keep-list names it.
	own := c.Own()
	outs = append(outs,
		Output{Path: path.Join(rel, EventsFile), Content: ev},
		Output{Path: path.Join(rel, AggregationFile), Content: aggregation.Generate(own, Header(c, "# "))},
		Output{Path: path.Join(rel, KeepFile), Content: Keep(c)},
	)
	want := map[string]bool{}
	for _, l := range langs {
		want[l] = true
	}
	git := GitRoot(root)
	for _, t := range c.Generate {
		if len(want) > 0 && !want[t.Lang] {
			continue
		}
		if err := checkOut(root, git, t.Lang, t.Out); err != nil {
			return nil, err
		}
		packageID, err := PackageIdentity(root, t)
		if err != nil {
			return nil, err
		}
		fs, err := Generate(t.Lang, own, Options{Package: t.Package, Namespace: t.Module, Interfaces: t.Interfaces, PackageID: packageID})
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
	resolvedOutputs := make(map[string]string, len(outs))
	for _, output := range outs {
		resolved := resolve(filepath.Join(root, filepath.FromSlash(output.Path)))
		if previous, exists := resolvedOutputs[resolved]; exists {
			return nil, fmt.Errorf("two outputs write %s and %s: the same resolved file", previous, output.Path)
		}
		resolvedOutputs[resolved] = output.Path
	}
	return outs, nil
}

// checkOut refuses an out: folder that leaves the repository: in a Git repository it must stay under the
// Git root (headers and the orphan walk are relative to it); with no repository it may not leave the
// project folder (the folder that holds observability/). Both the written path and its resolved path
// (symlinks followed as far as the folders exist) are checked, so a symlink cannot lead outside.
func checkOut(root, git, lang, out string) error {
	if filepath.IsAbs(out) || path.IsAbs(filepath.ToSlash(out)) {
		return fmt.Errorf("generate %s: out: %q must be relative to the project folder", lang, out)
	}
	target := filepath.Join(root, filepath.FromSlash(out))
	if git == "" {
		if rel := path.Clean(filepath.ToSlash(out)); rel == ".." || strings.HasPrefix(rel, "../") || !within(resolve(root), resolve(target)) {
			return fmt.Errorf("generate %s: out: %q leaves the project folder %s (no Git repository, so '..' and symlinks out are refused)", lang, out, root)
		}
		return nil
	}
	if !within(git, target) || !within(resolve(git), resolve(target)) {
		return fmt.Errorf("generate %s: out: %q leaves the git repository %s", lang, out, git)
	}
	return nil
}

// within reports whether p is base or below it.
func within(base, p string) bool {
	rel, err := filepath.Rel(base, p)
	return err == nil && rel != ".." && !strings.HasPrefix(filepath.ToSlash(rel), "../")
}

// resolve follows symlinks in the longest existing prefix of p and appends the rest unchanged.
func resolve(p string) string {
	p = filepath.Clean(p)
	rest := ""
	for {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return filepath.Join(r, rest)
		}
		parent := filepath.Dir(p)
		if parent == p {
			return filepath.Join(p, rest)
		}
		rest = filepath.Join(filepath.Base(p), rest)
		p = parent
	}
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
	plannedResolved := make(map[string]bool, len(outs))
	for _, o := range outs {
		planned[filepath.Join(root, filepath.FromSlash(o.Path))] = true
		plannedResolved[resolve(filepath.Join(root, filepath.FromSlash(o.Path)))] = true
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
	// The walk starts at the Git root (headers name repo-relative contract paths and out:
	// may point outside the project folder), or at the project folder outside Git.
	// Do not follow symlinks, dependency trees, git internals, or build caches; only the
	// file types obs writes are opened.
	selected := map[string]bool{}
	for _, lang := range langs {
		selected[lang] = true
	}
	all := len(langs) == 0
	contractPath := ContractPath(c)
	extensions := map[string]string{".go": "go", ".cs": "dotnet", ".dart": "dart", ".ts": "bun", ".yaml": ""}
	walkRoot := root
	if git := GitRoot(root); git != "" {
		walkRoot = git
	}
	err = filepath.WalkDir(walkRoot, func(p string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if p != walkRoot {
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
		if planned[p] {
			return nil
		}
		lang, known := extensions[filepath.Ext(p)]
		if !known || (!all && !selected[lang]) {
			return nil
		}
		if plannedResolved[resolve(p)] {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
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
