package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/AtomiCloud/obs/internal/obsyaml"
	"github.com/AtomiCloud/obs/internal/pins"
	"github.com/AtomiCloud/obs/internal/sandbox"
)

// loadObsYaml is the one obs.yaml loader every command uses. A missing file is (nil, true):
// the command keeps the versions and defaults built into this obs. A bad file prints the
// error (file, line, key) and returns false: the command exits 2.
func loadObsYaml(dir, cmd string, stderr io.Writer) (*obsyaml.File, bool) {
	f, err := obsyaml.Load(dir)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", cmd, err)
		return nil, false
	}
	return f, true
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
	fmt.Fprintf(stdout, "%s: %s: versions match the evaluators embedded in this obs%s\n", cmd, f.Path, appliedText(f))
	return 0
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
	ms := f.Mismatches()
	for _, m := range ms {
		fmt.Fprintf(stdout, "FAIL %s %s\n", f.Path, m)
	}
	for _, n := range f.Notes() {
		fmt.Fprintf(stdout, "note %s %s\n", f.Path, n)
	}
	if len(ms) == 0 {
		fmt.Fprintf(stdout, "obs.yaml %s: every version it names matches%s\n", f.Path, appliedText(f))
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
