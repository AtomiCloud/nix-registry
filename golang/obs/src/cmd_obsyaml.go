package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/AtomiCloud/obs/internal/contract"
	"github.com/AtomiCloud/obs/internal/obsyaml"
	"github.com/AtomiCloud/obs/internal/pins"
	"github.com/AtomiCloud/obs/internal/sandbox"
)

// loadObsYaml is the one obs.yaml loader every command uses. A missing file is (nil, true):
// the command keeps the versions and defaults built into this obs. A bad file prints the
// error (file, line, key) and returns false: the command exits 2.
func loadObsYaml(dir, cmd string, stderr io.Writer, scopes ...*contract.Scope) (*obsyaml.File, bool) {
	var f *obsyaml.File
	var err error
	if len(scopes) > 0 && scopes[0] != nil {
		f, err = obsyaml.LoadInRoot(dir, scopes[0].Root)
	} else {
		f, err = obsyaml.Load(dir)
	}
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", cmd, err)
		return nil, false
	}
	noteUnusedRoot(dir, f, cmd, stderr)
	return f, true
}

var rootNoted sync.Once

// noteUnusedRoot says once, on stderr, that the repository's root observability/obs.yaml exists and this run did not
// read it: a folder reads its own obs.yaml, or the one of an enclosing observability folder, and never an unrelated
// root file, so an app in a monorepo does not suddenly take the root's versions.
func noteUnusedRoot(dir string, f *obsyaml.File, cmd string, stderr io.Writer) {
	root := obsyaml.UnusedRootFile(dir, f)
	if root == "" {
		return
	}
	rootNoted.Do(func() {
		fmt.Fprintf(stderr, "%s: note: %s exists but %s/%s does not exist, so it is not used: this obs uses its built-in versions and thresholds here; copy %s to %s/%s or add one\n", cmd, root, dir, obsyaml.FileName, root, dir, obsyaml.FileName)
	})
}

// gateObsYaml is what `obs query` and `obs alerts` do with a loaded obs.yaml before any
// work: fail (return 1) when it names a store version the embedded evaluators do not
// match, else say in one line that it applies. A missing file prints nothing.
func gateObsYaml(f *obsyaml.File, cmd string, stdout io.Writer) int {
	if f == nil {
		return 0
	}
	if f.Gate(stdout, cmd) > 0 {
		return 1
	}
	fmt.Fprintf(stdout, "%s: %s: versions match the evaluators embedded in this obs%s%s\n", cmd, f.Path, inheritedText(f), appliedText(f))
	return 0
}

// inheritedText says where an obs.yaml came from when the folder asked for has none of its own.
func inheritedText(f *obsyaml.File) string {
	if f.Inherited {
		return " (the enclosing observability folder's: the folder asked for has no obs.yaml)"
	}
	return ""
}

func appliedText(f *obsyaml.File) string {
	if a := f.Applied(); len(a) > 0 {
		return "; thresholds applied: " + strings.Join(a, ", ")
	}
	return ""
}

// printObsYamlCheck is the version part of `obs check`: the evaluators this binary embeds,
// then what obs.yaml names against them. It returns the number of failures.
func printObsYamlCheck(f *obsyaml.File, dir string, stdout io.Writer) int {
	obsyaml.PrintEmbedded(stdout)
	if f == nil {
		fmt.Fprintf(stdout, "note: %s/%s not found: nothing to compare with the versions above; this obs uses its built-in versions and thresholds\n", dir, obsyaml.FileName)
		return 0
	}
	if f.Inherited {
		fmt.Fprintf(stdout, "note: %s/%s not found: using %s, the enclosing observability folder's\n", dir, obsyaml.FileName, f.Path)
	}
	ms := f.Mismatches()
	for _, m := range ms {
		fmt.Fprintf(stdout, "FAIL %s %s\n", f.Path, m)
	}
	for _, n := range f.Notes() {
		fmt.Fprintf(stdout, "note %s %s\n", f.Path, n)
	}
	if len(ms) == 0 {
		fmt.Fprintf(stdout, "obs.yaml %s: every version it names matches%s%s\n", f.Path, inheritedText(f), appliedText(f))
	}
	return len(ms)
}

// sandboxComponents is the stack `obs sandbox up` and `obs look` start: the images obs.yaml
// names, else the pins. A version with no known image digest is refused (exit 2) by name.
func sandboxComponents(f *obsyaml.File) ([]sandbox.Component, error) {
	if f == nil {
		return nil, nil
	}
	want := map[string]string{
		"grafana": f.Versions.Grafana, "victoriametrics": f.Versions.VictoriaMetrics,
		"victorialogs": f.Versions.VictoriaLogs, "tempo": f.Versions.Tempo, "pyroscope": f.Versions.Pyroscope,
	}
	comps, err := sandbox.Resolve(want, pins.All())
	if err != nil {
		return nil, fmt.Errorf("%s: %w", f.Path, err)
	}
	return comps, nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
