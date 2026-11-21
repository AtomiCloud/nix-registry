package contract

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// A repository may hold several contracts (apps/box/observability and apps/k8s-monitoring/observability):
// a dashboard of one reads metrics another contract declares. Discover finds the others, Clashes
// refuses a metric two contracts both declare, Merge gives one view for name lookups.

var skipDirs = map[string]bool{".git": true, ".direnv": true, "node_modules": true, "vendor": true, ".dart_tool": true, ".obs-cache": true}

// testDirs are folders of test material: a contract in one is a fixture, not part of the repository's observability.
var testDirs = map[string]bool{"fixtures": true, "testdata": true}

// Discover returns the other contract.yaml files of dir's repository: every contract.yaml in or below an
// observability folder, at any depth, below the Git root (observability/contract.yaml,
// observability/<feature>/contract.yaml, <feature>/observability/contract.yaml), except dir's own and any
// under a fixtures or testdata folder. A contract that is itself in such a folder, or in no Git repository,
// has no others. Sorted. A folder the walk cannot read is an error naming it (relative to the Git root):
// the others in it would go unchecked.
func Discover(dir string) ([]string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("contract discovery: %w", err)
	}
	root := gitRoot(abs)
	if root == "" {
		return nil, nil
	}
	return discoverAt(abs, root, false)
}

// DiscoverAll enumerates every contract, including dir itself, using the same
// pruning rules as Discover. A source export uses its topmost project boundary.
func DiscoverAll(dir string) ([]string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("contract discovery: %w", err)
	}
	root := gitRoot(abs)
	if root == "" {
		root = ProjectRoot(abs)
	}
	return discoverAt(abs, root, true)
}

// DiscoverProject is the source-export equivalent of Discover for generator
// scope checks. General single-folder query discovery keeps its Git semantics.
func DiscoverProject(dir string) ([]string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("contract discovery: %w", err)
	}
	return discoverAt(abs, ProjectRoot(abs), false)
}

// DiscoverScope uses project discovery in a source export only when requested
// by a repository-wide command. Existing single-folder commands stay Git-only.
func DiscoverScope(dir string, project bool) ([]string, error) {
	if project && gitRoot(dir) == "" {
		return DiscoverProject(dir)
	}
	return Discover(dir)
}

func discoverAt(abs, root string, includeOwn bool, scopes ...*Scope) ([]string, error) {
	if inTestDir(root, abs) {
		return nil, nil
	}
	own := filepath.Join(abs, "contract.yaml")
	ownInfo, _ := os.Stat(own)
	var scope *Scope
	var skip *walkSkip
	if len(scopes) > 0 && scopes[0] != nil {
		scope = scopes[0]
		skip = scope.Skip.w
	} else {
		skip = newWalkSkip(root, abs)
	}
	rootObs := rootIsObservability(root, scope)
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			cause := err
			var pe *fs.PathError
			if errors.As(err, &pe) {
				cause = pe.Err
			}
			rel, rerr := filepath.Rel(root, p)
			if rerr != nil {
				rel = p
			}
			return fmt.Errorf("contract discovery: cannot read %s: %w", filepath.ToSlash(rel), cause)
		}
		if d.IsDir() {
			if skip.dir(p, d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() == "contract.yaml" && d.Type().IsRegular() && (includeOwn || (p != own && !sameInfo(ownInfo, p))) && !skip.ignored[p] && belowObservability(root, filepath.Dir(p), rootObs, true) {
			out = append(out, p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	skip.note(len(out))
	return out, nil
}

// walkSkip is the one rule of which folders a walk of the repository never enters, shared by contract discovery and
// the observability folder scan (so a copy is neither a second contract nor a second definition of a rule uid):
//   - the dependency, tool and test folders (skipDirs, testDirs);
//   - a folder git ignores, pruned on entry so it is never read (an unreadable ignored build/ cannot fail a run), from
//     one call to git (ls-files --others --ignored --directory) made before the walk;
//   - a nested Git repository or submodule (a folder with its own .git): its own boundary, not this repository's;
//   - the files/ and templates/ of a folder that holds a Chart.yaml (a chart's copy of the alerts and dashboards).
//
// The selected folder (abs) and the folders above it are never skipped by the last three.
type walkSkip struct {
	root, abs string
	ignored   map[string]bool // absolute paths of the folders and files git ignores
	refused   bool            // git refused the repository for its ownership: nothing is known to be ignored
}

func newWalkSkip(root, abs string) *walkSkip {
	ign, refused := ignoredPaths(root)
	return &walkSkip{root: root, abs: abs, ignored: ign, refused: refused}
}

// note says, once, that ignored folders were not skipped because git refused the repository. It is said only when
// the walk found folders other than the selected one (others > 0): a repository with one observability folder has
// nothing that skipping could have changed, so it prints nothing new.
func (w *walkSkip) note(others int) {
	if !w.refused || others == 0 {
		return
	}
	gitNote.Do(func() {
		fmt.Fprintf(NoteWriter, "note: git refused to read %s (dubious ownership): folders git ignores are NOT skipped when obs counts observability folders, so an ignored copy of the alerts can clash; run: git config --global --add safe.directory %s\n", w.root, w.root)
	})
}

func (w *walkSkip) dir(p, name string) bool {
	if p == w.root {
		return false
	}
	if skipDirs[name] || testDirs[name] {
		return true
	}
	if p == w.abs || strings.HasPrefix(w.abs, p+string(filepath.Separator)) {
		return false
	}
	return w.below(p, name)
}

// below is the part of dir that also applies below an observability folder (no name lists): ignored, a nested
// repository, a chart's copy folders.
func (w *walkSkip) below(p, name string) bool {
	if w.ignored[p] || hasGit(p) {
		return true
	}
	return (name == "files" || name == "templates") && isChart(filepath.Dir(p))
}

// hasGit is true when folder p holds a .git (a directory, or the file of a submodule or a worktree).
func hasGit(p string) bool {
	_, err := os.Lstat(filepath.Join(p, ".git"))
	return err == nil
}

// rootIsObservability is true when the Git root is itself an observability folder: a standalone observability
// repository, recognised by what is at its top, never by the name of the folder it is checked out in (a clone
// may be called anything): an obs.yaml, or a contract.yaml that reads as an obs contract (a service: and one of
// the obs sections metrics:, logs:, spans:, platform_metrics:). A contract.yaml of something else (an OpenAPI
// file, a package manifest) leaves the repository an ordinary one.
func rootIsObservability(root string, scopes ...*Scope) bool {
	var scope *Scope
	if len(scopes) > 0 {
		scope = scopes[0]
	}
	if st, err := os.Stat(filepath.Join(root, "obs.yaml")); err == nil && st.Mode().IsRegular() && !scope.Skips(root, filepath.Join(root, "obs.yaml")) {
		return true
	}
	if scope.Skips(root, filepath.Join(root, "contract.yaml")) {
		return false
	}
	b, err := os.ReadFile(filepath.Join(root, "contract.yaml"))
	if err != nil {
		return false
	}
	var doc map[string]any
	if yaml.Unmarshal(b, &doc) != nil {
		return false
	}
	if svc, _ := doc["service"].(string); svc == "" {
		return false
	}
	for _, k := range []string{"metrics", "logs", "spans", "platform_metrics"} {
		if _, ok := doc[k]; ok {
			return true
		}
	}
	return false
}

// belowObservability is true when dir is an observability folder of the repository: a folder with a repo-relative
// path segment named observability (the folder itself or any folder above it, below the Git root), or, in a
// repository that is itself an observability folder (rootObs), the Git root and any folder below it that holds a
// contract.yaml (hasContract: an examples/ or docs/ folder of such a repository counts only for its contracts).
// Only paths relative to the Git root are read, so one repository gives one answer wherever it is checked out.
func belowObservability(root, dir string, rootObs, hasContract bool) bool {
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return false
	}
	if rel == "." {
		return rootObs
	}
	for _, seg := range strings.Split(filepath.ToSlash(rel), "/") {
		if seg == "observability" {
			return true
		}
	}
	return rootObs && hasContract
}

// sameInfo is true when path p is the file info says (nil info: no file).
func sameInfo(info os.FileInfo, p string) bool {
	if info == nil {
		return false
	}
	st, err := os.Stat(p)
	return err == nil && os.SameFile(info, st)
}

// Folders returns every observability folder of dir's repository that holds a contract.yaml or an alerts
// folder: a folder named observability or below one, at any depth, below the Git root, fixtures and
// testdata folders skipped. Copies are not folders of their own: a folder git ignores (one git ls-files call, pruned on entry) and the
// files/ and templates/ of a folder that holds a Chart.yaml (a chart's copy of the alerts and dashboards) are
// skipped, except dir itself. Sorted. dir itself is in the list when it qualifies. A dir that is in a
// fixtures or testdata folder, or in no Git repository, has none. An unreadable folder is an error naming it.
func Folders(dir string) ([]string, error) {
	out, _, err := FoldersSkip(dir)
	return out, err
}

// RepoSkip is the prune set of one walk of the repository (ignored folders, nested repositories, chart copies,
// dependency and test folders), kept so the files below a folder can be judged by the same rule: Skips is true for a
// path the walk would not have entered. A nil RepoSkip skips nothing.
type RepoSkip struct{ w *walkSkip }

// Skips reports whether the absolute path p (a file or a folder) below the observability folder folder lies in
// something the walk prunes: a folder git ignores, a nested repository, a chart's files/ or templates/. The name
// lists (vendor, fixtures, testdata, ...) are NOT applied: they decide which folders are observability folders, and
// below one an alert slug may be called vendor.
func (r *RepoSkip) Skips(folder, p string) bool {
	if r == nil || r.w == nil {
		return false
	}
	w := r.w
	if w.ignored[p] {
		return true
	}
	for d := filepath.Dir(p); d != folder && strings.HasPrefix(d, folder+string(filepath.Separator)); d = filepath.Dir(d) {
		if w.below(d, filepath.Base(d)) {
			return true
		}
	}
	return false
}

// FoldersSkip is Folders and the prune set it used (nil when dir has no repository), so a reader of the files below
// those folders applies the same ignore, nested-repository and copy rules without asking git again.
func FoldersSkip(dir string) ([]string, *RepoSkip, error) {
	return FoldersScopeSkip(dir, false)
}

// FoldersScopeSkip includes contract-less dashboard folders for repository-wide
// commands, and uses the project boundary when the source export has no Git.
func FoldersScopeSkip(dir string, project bool) ([]string, *RepoSkip, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("observability folders: %w", err)
	}
	root := gitRoot(abs)
	if root == "" && project {
		root = ProjectRoot(abs)
	}
	if root == "" || inTestDir(root, abs) {
		return nil, nil, nil
	}
	rootObs := rootIsObservability(root)
	skip := newWalkSkip(root, abs)
	var out []string
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			cause := err
			var pe *fs.PathError
			if errors.As(err, &pe) {
				cause = pe.Err
			}
			rel, rerr := filepath.Rel(root, p)
			if rerr != nil {
				rel = p
			}
			return fmt.Errorf("observability folders: cannot read %s: %w", filepath.ToSlash(rel), cause)
		}
		if !d.IsDir() {
			return nil
		}
		if skip.dir(p, d.Name()) {
			return filepath.SkipDir
		}
		st, serr := os.Stat(filepath.Join(p, "contract.yaml"))
		hasContract := serr == nil && st.Mode().IsRegular()
		hasData := false
		for _, name := range []string{"alerts", "dashboards"} {
			if name == "dashboards" && !project {
				continue
			}
			if st, err := os.Stat(filepath.Join(p, name)); err == nil && st.IsDir() {
				hasData = true
			}
		}
		if !belowObservability(root, p, rootObs, hasContract || (project && hasData)) {
			return nil
		}
		if hasContract || hasData {
			out = append(out, p)
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	sort.Strings(out)
	others := len(out)
	for _, o := range out {
		if o == abs {
			others--
		}
	}
	skip.note(others)
	return out, &RepoSkip{skip}, nil
}

// isChart is true when folder p holds a Chart.yaml.
func isChart(p string) bool {
	st, err := os.Stat(filepath.Join(p, "Chart.yaml"))
	return err == nil && st.Mode().IsRegular()
}

// ServiceStreamCount is the worst-case VictoriaLogs stream count of one service whose contracts are cs:
// landscapes x the distinct modules across them (a contract that names no module is one unnamed module).
// For one contract it is StreamCount.
func ServiceStreamCount(cs []*Contract, landscapes int) int64 {
	if landscapes < 1 {
		landscapes = 1
	}
	mods := map[string]bool{}
	for _, c := range cs {
		if len(c.Modules) == 0 {
			mods[""] = true
		}
		for _, m := range c.Modules {
			mods[m] = true
		}
	}
	return int64(landscapes) * int64(max(len(mods), 1))
}

func inTestDir(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	for _, seg := range strings.Split(filepath.ToSlash(rel), "/") {
		if testDirs[seg] {
			return true
		}
	}
	return false
}

func gitRoot(dir string) string {
	for d := dir; ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return d
		}
		if filepath.Dir(d) == d {
			return ""
		}
	}
}

// Clashes finds a stored series name that two contracts both own, in one table of stored names:
// a metric (ours or a source: one) against a metric of another contract, and a platform metric's
// possible stored names (histogram parts and unit variants included) against a metric of another
// contract. Platform metrics listed by several contracts are NOT a clash: every service lists
// http.server.request.duration, and Merge joins their label lists. Each issue names both files
// (relative to the Git root) and lines.
func Clashes(cs []*Contract) []Issue {
	type owner struct {
		c    *Contract
		name string
		line int
	}
	seen := map[string]owner{}
	var out []Issue
	for _, c := range cs {
		for _, n := range c.MetricOrder {
			m := c.Metrics[n]
			for _, s := range StoredNames(n, m) {
				if prev, dup := seen[s]; dup && prev.c != c {
					out = append(out, Issue{"metrics." + n, "metric-clash", fmt.Sprintf("metric %q (%s) and metric %q (%s) are both stored as the series %q: a series has one contract", n, c.at(m.Line), prev.name, prev.c.at(prev.line), s)})
					break
				} else if !dup {
					seen[s] = owner{c, n, m.Line}
				}
			}
		}
	}
	for _, c := range cs {
		for _, k := range c.platformKeys() {
			told := map[*Contract]bool{}
			for _, s := range platformStored(k) {
				if prev, dup := seen[s]; dup && prev.c != c && !told[prev.c] {
					told[prev.c] = true
					out = append(out, Issue{"platform_metrics." + k, "metric-clash", fmt.Sprintf("platform metric %q (%s) and metric %q (%s) are both stored as the series %q: a series has one contract", k, c.at(0), prev.name, prev.c.at(prev.line), s)})
				}
			}
		}
	}
	return out
}

// Merge is one contract for name lookups: the primary, plus every metric and platform metric of the
// others (with the types those metrics need, renamed "<contract file>/<type>" so two contracts' types
// never meet, even when both name the same service). A platform metric several contracts list gets the
// union of their labels. Logs, spans, versions and the hash stay the primary's. Neither input is
// changed. The result is a lookup view: it is not validated and not generated from.
func Merge(primary *Contract, others []*Contract) *Contract {
	m := *primary
	m.Metrics = copyMap(primary.Metrics)
	m.MetricOrder = append([]string(nil), primary.MetricOrder...)
	m.Types = copyMap(primary.Types)
	m.Platform = copyMap(primary.Platform)
	for _, o := range others {
		ns := o.File
		if ns == "" {
			ns = o.Service
		}
		for _, n := range o.MetricOrder {
			mt := o.Metrics[n]
			mt.Name = mt.NameOr(n)
			// The view is keyed by an internal key, not by stored name: two contracts may use one YAML key for
			// metrics with different stored names (a gauge queue_depth, a histogram queue_depth_bucket). A
			// stored name two contracts share is Clashes' to report, before Merge is called.
			key := freeKey(m.Metrics, ns, n)
			labels := map[string]string{}
			for l, t := range mt.Labels {
				if t == "" {
					labels[l] = ""
					continue
				}
				q := ns + "/" + t
				m.Types[q] = o.Types[t]
				labels[l] = q
			}
			mt.Labels = labels
			m.Metrics[key] = mt
			m.MetricOrder = append(m.MetricOrder, key)
		}
		for k, ls := range o.Platform {
			m.Platform[k] = union(m.Platform[k], ls)
		}
	}
	return &m
}

// freeKey is n when no metric of the view has it, else "<contract file>:<n>" (numbered if even that is taken):
// the key of a lookup view is internal; a metric is found by its stored names, never by its key.
func freeKey(taken map[string]Metric, ns, n string) string {
	if _, used := taken[n]; !used {
		return n
	}
	key := ns + ":" + n
	for i := 2; ; i++ {
		if _, used := taken[key]; !used {
			return key
		}
		key = fmt.Sprintf("%s:%s#%d", ns, n, i)
	}
}

// union is a followed by the entries of b that a lacks; a and b are not changed.
func union(a, b []string) []string {
	out := append([]string(nil), a...)
	for _, x := range b {
		if !in(out, x) {
			out = append(out, x)
		}
	}
	return out
}

func copyMap[K comparable, V any](in map[K]V) map[K]V {
	out := make(map[K]V, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// SameFile is true when a and b name one file or folder, however they are spelt (a symlinked --dir, a relative
// path, a hard link). A path that cannot be read is the same as nothing.
func SameFile(a, b string) bool {
	sa, err := os.Stat(a)
	if err != nil {
		return false
	}
	sb, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(sa, sb)
}

// NoteWriter is where the one note about git is written (stderr; tests replace it).
var NoteWriter io.Writer = os.Stderr

var gitNote sync.Once

// ignoredPaths returns the folders and files git ignores in the working tree of the Git root, by absolute path, from
// one call: git ls-files --others --ignored --exclude-standard --directory (an ignored folder is listed once, as
// itself, and is not read). Git missing, or the root not a repository git can read, ignores nothing: obs then
// counts every folder, as it did before. When git refuses the repository for its ownership (exit 128, "dubious
// ownership": a container CI running as another user than the checkout's), one note says that ignored folders are
// not skipped: it must not fail silently (when it could matter: see walkSkip.note).
func ignoredPaths(root string) (out map[string]bool, refused bool) {
	out = map[string]bool{}
	git, err := exec.LookPath("git")
	if err != nil {
		return out, false
	}
	cmd := exec.Command(git, "-C", root, "ls-files", "-z", "--others", "--ignored", "--exclude-standard", "--directory")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	b, err := cmd.Output()
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == 128 && (strings.Contains(stderr.String(), "dubious ownership") || strings.Contains(stderr.String(), "safe.directory")) {
		return out, true // the caller says so once, when it matters (walkSkip.note)
	}
	if err != nil {
		return out, false // not a repository git can read (a fake .git), or any other failure: nothing is ignored
	}
	for _, rel := range strings.Split(string(b), "\x00") {
		if rel = strings.TrimSuffix(rel, "/"); rel != "" {
			out[filepath.Join(root, filepath.FromSlash(rel))] = true
		}
	}
	return out, false
}
