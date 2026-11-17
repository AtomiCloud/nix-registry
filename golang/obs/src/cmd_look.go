package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"

	"github.com/AtomiCloud/obs/internal/look"
	"github.com/AtomiCloud/obs/internal/sandbox"
)

// runLook is `obs look`: setup and a prompt for the calling agent. It calls no LLM, reads
// no key and posts nothing. It exits 0 even when the setup fails (it prints what failed);
// only a bad flag is an error (exit 2), because that is a mistake in the caller's own script.
func runLook(args []string, stdout, stderr io.Writer) (code int) {
	fs := flag.NewFlagSet("obs look", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var f sandboxFlags
	f.register(fs)
	base := fs.String("base", "origin/main", "git ref the changed dashboards are measured against (merge base, so edits and new files count)")
	all := fs.Bool("all", false, "review every dashboard, not only the changed ones")
	checklist := fs.String("checklist", "docs/standards/grafana-dashboards/index.md", "the visual checklist (a markdown file with a \"Visual checklist\" bullet list)")
	out := fs.String("out", "obs-look", "folder the screenshots are saved in (as written in the prompt)")
	width := fs.Int("width", 1600, "viewport width in px")
	height := fs.Int("height", 1000, "viewport height in px")
	asJSON := fs.Bool("json", false, "print the same content as JSON (with the prompt in the \"prompt\" field)")
	var files multiFlag
	fs.Var(&files, "file", "a dashboard file, relative to --dir (repeatable; skips git)")
	if err := fs.Parse(args); err != nil {
		return 2
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
		cfg, err := f.config(stderr)
		if err != nil {
			return nil, err
		}
		return sandbox.Up(ctx, cfg)
	}
	plan := look.Setup(ctx, look.Options{
		Dir: f.dir, SeedPath: f.seed, Base: *base, All: *all, Files: files, Checklist: *checklist,
		Out: *out, Width: *width, Height: *height,
	}, up)
	if *asJSON {
		b, _ := json.MarshalIndent(plan, "", "  ")
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	fmt.Fprint(stdout, plan.Prompt)
	return 0
}
