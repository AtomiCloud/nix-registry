package contract

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
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

// Discover returns the other contract.yaml files of dir's repository: every observability/contract.yaml
// below the Git root, except dir's own and any under a fixtures or testdata folder. A contract that
// is itself in such a folder, or in no Git repository, has no others. Sorted. A folder the walk cannot
// read is an error naming it (relative to the Git root): the others in it would go unchecked.
func Discover(dir string) ([]string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("contract discovery: %w", err)
	}
	own := filepath.Join(abs, "contract.yaml")
	root := gitRoot(abs)
	if root == "" || inTestDir(root, abs) {
		return nil, nil
	}
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
		if d.Name() == "contract.yaml" && p != own && filepath.Base(filepath.Dir(p)) == "observability" && d.Type().IsRegular() {
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
