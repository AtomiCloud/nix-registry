package main

// OBS_VERIFY_PRUNE=1 for `obs alerts`: the third self-check, next to
// OBS_VERIFY_FAST and OBS_VERIFY_SKIP. Those two compare two evaluators over the
// SAME already-pruned input, so they cannot see a wrong lazy-load decision (the
// union bug: pruned count 117, full 234, both reported 0 mismatches). This one
// evaluates the whole project twice, once with the default lazy log/metric/trace
// pruning and once with OBS_NO_PRUNE=1, and compares everything that comes out.
//
// The VictoriaMetrics/VictoriaLogs engines keep process globals, so the two
// evaluations are two isolated child processes of this same executable, on the
// same fixed --base, with the result cache bypassed (--steps never reads or
// writes it and OBS_CACHE_DIR is dropped from the child environment).

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/AtomiCloud/obs/internal/alerts"
	"github.com/AtomiCloud/obs/internal/files"
)

// snapshotEnv is the hook the alerts package honours: when set to a path, the run
// writes its raw cached datasource results, per query input, as JSON there.
const snapshotEnv = "OBS_VERIFY_PRUNE_SNAPSHOT"

// plantSeedEnv is for the self-check's own test only: the pruned child reads this
// seed file instead of the project's, so the comparison must go red and say where.
const plantSeedEnv = "OBS_VERIFY_PRUNE_PLANT_SEED"

// maxPruneReport caps the diff lines printed per section; the count stays exact.
const maxPruneReport = 20

type pruneChild struct {
	name                           string
	rc                             int
	stdout, stderr                 string
	steps, recorded, out, snapshot string
}

// childEnv drops every switch that would change what the child evaluates or whether
// it reads a cache, then adds extra.
func childEnv(env []string, extra ...string) []string {
	drop := map[string]bool{
		"OBS_VERIFY_PRUNE": true, "OBS_NO_PRUNE": true, "OBS_CACHE_DIR": true, snapshotEnv: true, plantSeedEnv: true,
	}
	var out []string
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if !drop[k] {
			out = append(out, kv)
		}
	}
	return append(out, extra...)
}

func runPruneChild(exe, tmp, name string, o alerts.Options, base int64, noPrune bool) (*pruneChild, error) {
	c := &pruneChild{
		name:     name,
		steps:    filepath.Join(tmp, name+"-steps.json"),
		recorded: filepath.Join(tmp, name+"-recorded.json"),
		out:      filepath.Join(tmp, name+"-out.json"),
		snapshot: filepath.Join(tmp, name+"-snapshot.json"),
	}
	args := []string{"alerts", "--dir", o.Dir, "--steps", c.steps, "--recorded", c.recorded, "--out", c.out}
	if o.SeedPath != "" {
		args = append(args, "--seed", o.SeedPath)
	}
	if base != 0 {
		args = append(args, "--base", strconv.FormatInt(base, 10))
	}
	if o.Workers > 0 {
		args = append(args, "--workers", strconv.Itoa(o.Workers))
	}
	extra := []string{snapshotEnv + "=" + c.snapshot}
	if noPrune {
		extra = append(extra, "OBS_NO_PRUNE=1")
	}
	cmd := exec.Command(exe, args...)
	cmd.Env = childEnv(os.Environ(), extra...)
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	err := cmd.Run()
	c.stdout, c.stderr = so.String(), se.String()
	if err == nil {
		return c, nil
	}
	if ee, ok := err.(*exec.ExitError); ok {
		c.rc = ee.ExitCode()
		return c, nil
	}
	return nil, fmt.Errorf("start %s child: %w", name, err)
}

func readOpt(path string) ([]byte, bool) {
	b, err := os.ReadFile(path)
	return b, err == nil
}

// ruleIndex maps a rule id to the files and query expressions that explain a diff.
func ruleIndex(dir string) map[string][]string {
	idx := map[string][]string{}
	set, err := files.Load(dir)
	if err != nil {
		return idx
	}
	re := regexp.MustCompile(`^rule (\S+) query (\S+)`)
	for _, q := range set.Queries {
		if !q.IsAlert {
			continue
		}
		m := re.FindStringSubmatch(q.Where)
		if m == nil {
			continue
		}
		idx[m[1]] = append(idx[m[1]], fmt.Sprintf("%s rule %s query %s: %s", q.File, m[1], m[2], q.Expr))
	}
	return idx
}

func runAlertsVerifyPrune(o alerts.Options, stdout, stderr io.Writer) int {
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintln(stderr, "obs alerts verify-prune:", err)
		return 2
	}
	tmp, err := os.MkdirTemp("", "obs-verify-prune-")
	if err != nil {
		fmt.Fprintln(stderr, "obs alerts verify-prune:", err)
		return 2
	}
	defer os.RemoveAll(tmp)

	prunedOpts := o
	if p := os.Getenv(plantSeedEnv); p != "" {
		prunedOpts.SeedPath = p
	}
	pruned, err := runPruneChild(exe, tmp, "pruned", prunedOpts, o.Base, false)
	if err != nil {
		fmt.Fprintln(stderr, "obs alerts verify-prune:", err)
		return 2
	}
	// Setup errors (bad project, base in the future) are the same for both runs.
	if pruned.rc == 2 {
		io.WriteString(stdout, pruned.stdout)
		io.WriteString(stderr, pruned.stderr)
		return 2
	}
	base := o.Base
	if base == 0 {
		raw, _ := readOpt(pruned.out)
		ms, ok := baseFromOut(raw)
		if !ok {
			fmt.Fprintln(stderr, "obs alerts verify-prune: the child's --out JSON has no base_ms, so the two runs cannot share one base; pass --base <unix seconds>")
			return 2
		}
		base = ms / 1000
	}
	// The pruned child chose its own base when none was given; the unpruned one reuses it.
	full, err := runPruneChild(exe, tmp, "full", o, base, true)
	if err != nil {
		fmt.Fprintln(stderr, "obs alerts verify-prune:", err)
		return 2
	}
	if full.rc == 2 {
		fmt.Fprintf(stderr, "obs alerts verify-prune: the OBS_NO_PRUNE=1 run failed to start (exit 2) after the pruned run succeeded:\n%s%s", full.stdout, full.stderr)
		return 2
	}

	io.WriteString(stdout, pruned.stdout)
	io.WriteString(stderr, pruned.stderr)

	rules := ruleIndex(o.Dir)
	baseTime := time.Unix(base, 0).UTC()
	var report []string
	total, compared := 0, 0
	section := func(name string, diffs []pruneDiff, st cmpStats, explain func(pruneDiff) string) {
		compared += st.Compared
		total += len(diffs)
		for i, d := range diffs {
			if i == maxPruneReport {
				report = append(report, fmt.Sprintf("  ... %d more %s difference(s)", len(diffs)-maxPruneReport, name))
				break
			}
			report = append(report, fmt.Sprintf("MISMATCH %s %s", name, d))
			if explain != nil {
				if e := explain(d); e != "" {
					report = append(report, "  "+e)
				}
			}
		}
	}
	explained := map[string]bool{}
	explainAlert := func(d pruneDiff) string {
		id, ok := strings.CutPrefix(d.Path[0], "alert ")
		if !ok || explained[id] {
			return ""
		}
		explained[id] = true
		return "rule " + id + " queries: " + strings.Join(rules[id], "; ")
	}

	if pruned.rc != full.rc {
		total++
		report = append(report, fmt.Sprintf("MISMATCH exit status: pruned=%d full=%d (the pruned run and the OBS_NO_PRUNE=1 run disagree on pass/fail)", pruned.rc, full.rc))
	}

	// Per-minute states, values, errors, transitions.
	sp, errP := readOpt(pruned.steps)
	sf, errF := readOpt(full.steps)
	if errP && errF {
		np, e1 := normalizeSteps(sp, baseTime)
		nf, e2 := normalizeSteps(sf, baseTime)
		if e1 != nil || e2 != nil {
			fmt.Fprintln(stderr, "obs alerts verify-prune:", e1, e2)
			return 2
		}
		var diffs []pruneDiff
		var st cmpStats
		diffTree(nil, np, nf, &st, &diffs)
		section("steps", diffs, st, explainAlert)
	} else {
		total++
		report = append(report, fmt.Sprintf("MISMATCH steps: file written by pruned=%t full=%t", errP, errF))
	}

	// Verdicts and failed count.
	op, okP := readOpt(pruned.out)
	of, okF := readOpt(full.out)
	if okP && okF {
		np, e1 := normalizeVerdicts(op)
		nf, e2 := normalizeVerdicts(of)
		if e1 != nil || e2 != nil {
			fmt.Fprintln(stderr, "obs alerts verify-prune:", e1, e2)
			return 2
		}
		var diffs []pruneDiff
		var st cmpStats
		diffTree(nil, np, nf, &st, &diffs)
		section("verdicts", diffs, st, nil)
	} else {
		total++
		report = append(report, fmt.Sprintf("MISMATCH verdicts: file written by pruned=%t full=%t", okP, okF))
	}

	// Recording-rule samples.
	rp, okP := readOpt(pruned.recorded)
	rf, okF := readOpt(full.recorded)
	if okP && okF {
		np, e1 := normalizeRecorded(rp)
		nf, e2 := normalizeRecorded(rf)
		if e1 != nil || e2 != nil {
			fmt.Fprintln(stderr, "obs alerts verify-prune:", e1, e2)
			return 2
		}
		var diffs []pruneDiff
		var st cmpStats
		diffTree(nil, np, nf, &st, &diffs)
		section("recorded", diffs, st, nil)
	} else {
		total++
		report = append(report, fmt.Sprintf("MISMATCH recorded: file written by pruned=%t full=%t", okP, okF))
	}

	// Raw datasource results per query input (written by the alerts package hook).
	snapshots := 0
	cp, okP := readOpt(pruned.snapshot)
	cf, okF := readOpt(full.snapshot)
	switch {
	case okP && okF:
		vp, e1 := decodeJSON(cp)
		vf, e2 := decodeJSON(cf)
		if e1 != nil || e2 != nil {
			fmt.Fprintln(stderr, "obs alerts verify-prune: snapshot JSON:", e1, e2)
			return 2
		}
		if m, ok := vp.(map[string]any); ok {
			snapshots = len(m)
		}
		var diffs []pruneDiff
		var st cmpStats
		diffTree([]string{"query input"}, vp, vf, &st, &diffs)
		section("query-input", diffs, st, nil)
	case okP != okF:
		total++
		report = append(report, fmt.Sprintf("MISMATCH query-input snapshot: file written by pruned=%t full=%t", okP, okF))
	default:
		report = append(report, "note: neither run wrote an "+snapshotEnv+" snapshot (hook not present in this build); query inputs are compared through the per-minute results only")
	}

	for _, l := range report {
		fmt.Fprintln(stdout, l)
	}
	fmt.Fprintf(stdout, "verify prune: base=%d compared=%d query_inputs=%d mismatches=%d (lazy pruning vs OBS_NO_PRUNE=1)\n", base, compared, snapshots, total)

	for _, c := range []struct{ src, dst string }{{pruned.steps, o.Steps}, {pruned.recorded, o.Recorded}, {pruned.out, o.Out}} {
		if c.dst == "" {
			continue
		}
		if b, ok := readOpt(c.src); ok {
			if err := os.WriteFile(filepath.Clean(c.dst), b, 0o644); err != nil {
				fmt.Fprintln(stderr, err)
				return 2
			}
		}
	}
	if total > 0 {
		return 1
	}
	return pruned.rc
}
