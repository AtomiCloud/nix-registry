package contract

import (
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
// is itself in such a folder, or in no Git repository, has no others. Sorted.
func Discover(dir string) []string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil
	}
	own := filepath.Join(abs, "contract.yaml")
	root := gitRoot(abs)
	if root == "" || inTestDir(root, abs) {
		return nil
	}
	var out []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
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
	sort.Strings(out)
	return out
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

// Clashes finds a stored series name two contracts both declare (or a platform metric both list).
// Each issue names both files and lines.
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
		for _, k := range c.platformKeys() {
			key := "platform:" + StoredBase(k)
			if prev, dup := seen[key]; dup && prev.c != c {
				out = append(out, Issue{"platform_metrics." + k, "metric-clash", fmt.Sprintf("platform metric %q (%s) is also listed in %s", k, c.File, prev.c.File)})
			} else if !dup {
				seen[key] = owner{c, k, 0}
			}
		}
	}
	return out
}

// Merge is one contract for name lookups: the primary, plus every metric and platform metric of the
// others (with the types those metrics need, renamed "<service>/<type>" so two contracts' types
// never meet). Logs, spans, versions and the hash stay the primary's. Neither input is changed.
// The result is a lookup view: it is not validated and not generated from.
func Merge(primary *Contract, others []*Contract) *Contract {
	m := *primary
	m.Metrics = copyMap(primary.Metrics)
	m.MetricOrder = append([]string(nil), primary.MetricOrder...)
	m.Types = copyMap(primary.Types)
	m.Platform = copyMap(primary.Platform)
	for _, o := range others {
		for _, n := range o.MetricOrder {
			if _, taken := m.Metrics[n]; taken {
				continue // a clash is Clashes' to report
			}
			mt := o.Metrics[n]
			labels := map[string]string{}
			for l, t := range mt.Labels {
				if t == "" {
					labels[l] = ""
					continue
				}
				q := o.Service + "/" + t
				m.Types[q] = o.Types[t]
				labels[l] = q
			}
			mt.Labels = labels
			m.Metrics[n] = mt
			m.MetricOrder = append(m.MetricOrder, n)
		}
		for k, ls := range o.Platform {
			if _, taken := m.Platform[k]; !taken {
				m.Platform[k] = ls
			}
		}
	}
	return &m
}

func copyMap[K comparable, V any](in map[K]V) map[K]V {
	out := make(map[K]V, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
