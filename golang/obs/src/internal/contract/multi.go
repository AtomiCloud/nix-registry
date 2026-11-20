package contract

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
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
	own := filepath.Join(abs, "contract.yaml")
	ownInfo, _ := os.Stat(own) // a symlinked --dir spells its own contract another way: compare the file, not the path
	root := gitRoot(abs)
	if root == "" || inTestDir(root, abs) {
		return nil, nil
	}
	rootObs := rootIsObservability(root)
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
			return fmt.Errorf("contract discovery: cannot read %s: %w", filepath.ToSlash(rel), cause)
		}
		if d.IsDir() {
			if p != root && (skipDirs[d.Name()] || testDirs[d.Name()]) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() == "contract.yaml" && p != own && d.Type().IsRegular() && !sameInfo(ownInfo, p) && belowObservability(root, filepath.Dir(p), rootObs, true) {
			out = append(out, p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

// rootIsObservability is true when the Git root is itself an observability folder: a standalone observability
// repository, recognised by what is at its top (a contract.yaml or an obs.yaml), never by the name of the folder
// it is checked out in (a clone may be called anything).
func rootIsObservability(root string) bool {
	for _, n := range []string{"contract.yaml", "obs.yaml"} {
		if st, err := os.Stat(filepath.Join(root, n)); err == nil && st.Mode().IsRegular() {
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
// testdata folders skipped. Copies are not folders of their own: a folder git ignores (git check-ignore) and
// anything below a folder that holds a Chart.yaml (a chart's files/ copy of the alerts and dashboards) is
// skipped, except dir itself. Sorted. dir itself is in the list when it qualifies. A dir that is in a
// fixtures or testdata folder, or in no Git repository, has none. An unreadable folder is an error naming it.
func Folders(dir string) ([]string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("observability folders: %w", err)
	}
	root := gitRoot(abs)
	if root == "" || inTestDir(root, abs) {
		return nil, nil
	}
	rootObs := rootIsObservability(root)
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
		if p != root && (skipDirs[d.Name()] || testDirs[d.Name()]) {
			return filepath.SkipDir
		}
		if p != root && p != abs && isChart(p) {
			return filepath.SkipDir // a chart's own files are copies of what the repository's folders hold
		}
		st, serr := os.Stat(filepath.Join(p, "contract.yaml"))
		hasContract := serr == nil && st.Mode().IsRegular()
		if !belowObservability(root, p, rootObs, hasContract) {
			return nil
		}
		if hasContract {
			out = append(out, p)
		} else if st, serr := os.Stat(filepath.Join(p, "alerts")); serr == nil && st.IsDir() {
			out = append(out, p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	ign := ignoredBy(root, out)
	kept := out[:0]
	for _, f := range out {
		if f == abs || !ign[f] {
			kept = append(kept, f)
		}
	}
	return kept, nil
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

// ignoredBy returns the folders git ignores (git check-ignore, run in the Git root), by path. Git missing, or
// the root not a repository git can read, ignores nothing: obs then counts every folder, as it did before.
func ignoredBy(root string, folders []string) map[string]bool {
	out := map[string]bool{}
	if len(folders) == 0 {
		return out
	}
	git, err := exec.LookPath("git")
	if err != nil {
		return out
	}
	var in bytes.Buffer
	for _, f := range folders {
		rel, err := filepath.Rel(root, f)
		if err != nil || rel == "." {
			continue
		}
		in.WriteString(filepath.ToSlash(rel))
		in.WriteByte(0)
	}
	cmd := exec.Command(git, "-C", root, "check-ignore", "-z", "--stdin")
	cmd.Stdin = &in
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	b, _ := cmd.Output() // exit 1 = nothing ignored, 128 = not a repository: both mean no ignored folder
	for _, rel := range strings.Split(string(b), "\x00") {
		if rel != "" {
			out[filepath.Join(root, filepath.FromSlash(rel))] = true
		}
	}
	return out
}
