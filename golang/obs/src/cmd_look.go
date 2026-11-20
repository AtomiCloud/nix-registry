package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/AtomiCloud/obs/internal/look"
	"github.com/AtomiCloud/obs/internal/sandbox"
)

// runLook is `obs look`: setup and a prompt for the calling agent. It calls no LLM, reads
// no key and posts nothing. It exits 0 always: a failed setup, a bad flag and --help print
// what went wrong (or the usage) and never fail the CI job that calls it.
func runLook(args []string, stdout, stderr io.Writer) (code int) {
	fs := flag.NewFlagSet("obs look", flag.ContinueOnError)
	var usage bytes.Buffer
	fs.SetOutput(&usage)
	var f sandboxFlags
	f.register(fs)
	base := fs.String("base", "origin/main", "git ref the changed dashboards are measured against (merge base, so edits and new files count)")
	all := fs.Bool("all", false, "review every dashboard, not only the changed ones")
	keep := fs.Bool("keep", false, "leave the sandbox running for a person after the calling agent reviews it")
	seedBase := fs.Int64("seed-base", 0, "explicit seed start as unix seconds (historical replay); default places the latest incident over now")
	checklist := fs.String("checklist", "docs/standards/grafana-dashboards/index.md", "the visual checklist (a markdown file with a \"Visual checklist\" bullet list)")
	out := fs.String("out", "obs-look", "folder the screenshots are saved in (as written in the prompt)")
	width := fs.Int("width", 1600, "viewport width in px")
	height := fs.Int("height", 1000, "viewport height in px")
	asJSON := fs.Bool("json", false, "print the same content as JSON (with the prompt in the \"prompt\" field)")
	var files multiFlag
	fs.Var(&files, "file", "a dashboard file, relative to --dir (repeatable; skips git)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(stdout, usage.String())
			return 0
		}
		// A usage error does not fail CI either: say what is wrong, exit 0.
		msg := "usage error: " + err.Error()
		prompt := "obs look: not run, " + msg + ". obs look exits 0 on purpose; this is not a CI failure.\n\n" + usage.String()
		if wantsJSON(args) {
			b, _ := json.MarshalIndent(&look.Plan{Error: msg, Checklist: []string{}, Dashboards: []look.Dashboard{}, Prompt: prompt}, "", "  ")
			fmt.Fprintln(stdout, string(b))
			return 0
		}
		fmt.Fprint(stdout, prompt)
		return 0
	}
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(stdout, "obs look: setup failed, nothing to review (internal error: %v). obs look exits 0 on purpose.\n", r)
			code = 0
		}
	}()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	up := func(ctx context.Context) (*sandbox.Info, error) {
		cfg, err := f.upConfig(stderr, stderr)
		if err != nil {
			return nil, err
		}
		cfg.ApplyIncidents = true
		cfg.LiveIncident = *seedBase == 0
		cfg.SeedBase = *seedBase
		return sandbox.Up(ctx, cfg)
	}
	plan := look.Setup(ctx, look.Options{
		Dir: f.dir, SeedPath: f.seed, Base: *base, All: *all, Keep: *keep, Files: files, Checklist: *checklist,
		Out: *out, Width: *width, Height: *height,
		// The cleanup command in the prompt names the project and runtime this run uses.
		Project: projectName(f.project, f.dir), Runtime: runtimeName(f.runtime),
	}, up)
	if *asJSON {
		b, _ := json.MarshalIndent(plan, "", "  ")
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	fmt.Fprint(stdout, plan.Prompt)
	return 0
}

// wantsJSON tells whether --json (or -json, --json=true) is on the line, even when parsing failed before it.
func wantsJSON(args []string) bool {
	for _, a := range args {
		switch strings.TrimLeft(a, "-") {
		case "json", "json=true":
			if strings.HasPrefix(a, "-") {
				return true
			}
		}
	}
	return false
}

// runtimeName is the container runtime this run uses (docker or podman), as `--runtime` takes it.
// When none can be found the setup fails on its own and the flag value (maybe empty) is printed.
func runtimeName(flagValue string) string {
	bin, err := sandbox.DetectRuntime(flagValue)
	if err != nil {
		return flagValue
	}
	return filepath.Base(bin)
}
