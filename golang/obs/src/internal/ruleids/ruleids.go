// Package ruleids checks that no two alert rules of one repository share a rule uid. Grafana keeps one rule per
// uid, so a second feature shipping the same uid would overwrite the first. obs alerts has always checked one
// folder (--dir); with a contract per feature a repository holds several alert folders, so the check reads
// them all: every observability folder in or below the Git root that holds an alerts folder or a contract.
package ruleids

import (
	"fmt"
	"path/filepath"
	"sort"

	"github.com/AtomiCloud/obs/internal/contract"
	"github.com/AtomiCloud/obs/internal/files"
)

// Rule is where one rule uid is defined.
type Rule struct {
	ID   string
	File string // the alert file, relative to the Git root
}

// Clash is one uid two alert files define.
type Clash struct {
	ID          string
	First, Also string // alert files relative to the Git root; First is the earlier in path order
}

func (c Clash) String() string {
	return fmt.Sprintf("rule %s: [rule-id-clash] uid %q is also defined in %s: Grafana keeps one rule per uid, so one of the two would overwrite the other", c.Also, c.ID, c.First)
}

// ID is the identity of a rule: its uid, else the metadata.name of its file (the same fallback the query reader uses).
func ID(r files.Rule) string {
	if r.Definition.UID != "" {
		return r.Definition.UID
	}
	return r.Name
}

// Others returns the rules of every other observability folder of dir's repository (dir's own excluded).
// A folder that cannot be read is an error naming it. An alert file that cannot be parsed is that folder's own
// check to report: its rules are the ones that did parse.
func Others(dir string) ([]Rule, error) {
	out, _, err := OthersSkip(dir)
	return out, err
}

// OthersSkip is Others and the prune set it used, so the caller can apply the same rule to the rules of dir itself
// (contract.RepoSkip.Skips(dir, file)). The set is nil outside a Git repository.
func OthersSkip(dir string) ([]Rule, *contract.RepoSkip, error) {
	return OthersScopeSkip(dir, false)
}

// OthersScopeSkip extends the same uid check to source exports in --all mode.
func OthersScopeSkip(dir string, project bool) ([]Rule, *contract.RepoSkip, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, nil, err
	}
	folders, skip, err := contract.FoldersScopeSkip(abs, project)
	if err != nil {
		return nil, nil, err
	}
	var out []Rule
	for _, f := range folders {
		if f == abs || contract.SameFile(f, abs) { // a symlinked --dir is the same folder as its target
			continue
		}
		rs, err := read(f, skip)
		if err != nil {
			return nil, nil, err
		}
		out = append(out, rs...)
	}
	return out, skip, nil
}

// All returns the rules of every observability folder of dir's repository, dir's own included, and the clashes among
// them. Nothing is returned for a dir with no other folder: a repository with one observability folder is checked
// by the per-folder duplicate check alone.
func All(dir string) ([]Clash, error) {
	return AllScope(dir, false)
}

// AllScope uses the same project boundary as repository-wide commands.
func AllScope(dir string, project bool) ([]Clash, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	folders, skip, err := contract.FoldersScopeSkip(abs, project)
	if err != nil {
		return nil, err
	}
	if len(folders) < 2 {
		return nil, nil
	}
	var all []Rule
	for _, f := range folders {
		rs, err := read(f, skip)
		if err != nil {
			return nil, err
		}
		all = append(all, rs...)
	}
	return Find(all, nil), nil
}

// Find returns the clashes among rules (a uid defined in two different files), in the order of the later file. Rules
// of one file with one uid are the per-folder check's to report, not this one's. When mine is not nil only the
// clashes with a rule of mine (a rule of this folder) are returned.
func Find(rules []Rule, mine map[string]bool) []Clash {
	first := map[string]Rule{}
	var out []Clash
	for _, r := range rules {
		prev, dup := first[r.ID]
		if !dup {
			first[r.ID] = r
			continue
		}
		if prev.File == r.File {
			continue
		}
		if mine != nil && !mine[prev.File] && !mine[r.File] {
			continue
		}
		out = append(out, Clash{ID: r.ID, First: prev.File, Also: r.File})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Also < out[j].Also })
	return out
}

// read is the rules of one observability folder, files named relative to the Git root. A rule file below a folder
// the discovery walk prunes (git-ignored, a nested repository, a chart's copy) is not a definition of its uid: the
// folder's alerts/ is read with the same rule as the folders themselves.
func read(folder string, skip *contract.RepoSkip) ([]Rule, error) {
	set, err := files.LoadSkip(folder, func(p string) bool { return skip.Skips(folder, p) }) // pruned before it is read
	if err != nil {
		return nil, err
	}
	var out []Rule
	for _, r := range set.Rules {
		id := ID(r)
		if id == "" {
			continue // a rule with no uid and no name is that folder's own failure
		}
		out = append(out, Rule{ID: id, File: contract.RelPath(filepath.Join(folder, r.File))})
	}
	return out, nil
}
