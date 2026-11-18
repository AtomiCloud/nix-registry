package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"

	"github.com/AtomiCloud/obs/internal/obsyaml"
	"github.com/AtomiCloud/obs/internal/sandbox"
)

const sandboxUsage = `usage:
  obs sandbox up     [--dir observability] [--seed seed.yaml] [--project name] [--port-base n] [--runtime docker|podman] [--reseed]
  obs sandbox down   [--dir observability] [--project name] [--runtime docker|podman]
  obs sandbox status [--dir observability] [--project name]
`

// projectName is the sandbox label value: --project, else the folder that holds the
// observability folder (the repo name).
func projectName(flagValue, dir string) string {
	if flagValue != "" {
		return sandbox.ProjectName(flagValue)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "default"
	}
	return sandbox.ProjectName(filepath.Base(filepath.Dir(abs)))
}

// sandboxFlags are the flags `obs sandbox` and `obs look` share.
type sandboxFlags struct {
	dir, seed, project, runtime string
	portBase                    int
	reseed                      bool
}

func (f *sandboxFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&f.dir, "dir", "observability", "the observability folder (dashboards, seed)")
	fs.StringVar(&f.seed, "seed", "", "seed file (default <dir>/seed.yaml)")
	fs.StringVar(&f.project, "project", "", "sandbox name, the obs.sandbox label value (default: the repo folder's name)")
	fs.StringVar(&f.runtime, "runtime", "", "container runtime: docker or podman (default $OBS_CONTAINER_RUNTIME, else docker, else podman)")
	fs.IntVar(&f.portBase, "port-base", 0, "host port of Grafana; VictoriaMetrics, VictoriaLogs, Tempo and Pyroscope follow at +1..+4 (default: their own ports, 3000 8428 9428 3200 4040)")
	fs.BoolVar(&f.reseed, "reseed", false, "load the seed again into a sandbox that already has it")
}

// upConfig is config for a run that starts the stack: the images are the ones obs.yaml
// names (<dir>/obs.yaml), else the pins. A bad obs.yaml or a version with no known image
// digest is an error that names the key. down and status never read obs.yaml, so a broken
// file cannot stop a tear-down.
func (f *sandboxFlags) upConfig(out io.Writer) (sandbox.Config, error) {
	// obs.yaml first: a bad file or an unpinned version is refused before any runtime is looked for.
	of, err := obsyaml.Load(f.dir)
	if err != nil {
		return sandbox.Config{}, err
	}
	comps, err := sandboxComponents(of)
	if err != nil {
		return sandbox.Config{}, err
	}
	cfg, err := f.config(out)
	if err != nil {
		return cfg, err
	}
	cfg.Components = comps
	if of != nil {
		fmt.Fprintf(out, "images from %s:", of.Path)
		for _, c := range cfg.Components {
			fmt.Fprintf(out, " %s", c.Ref())
		}
		fmt.Fprintln(out)
	}
	return cfg, nil
}

func (f *sandboxFlags) config(out io.Writer) (sandbox.Config, error) {
	bin, err := sandbox.DetectRuntime(f.runtime)
	if err != nil {
		return sandbox.Config{}, err
	}
	return sandbox.Config{
		Project: projectName(f.project, f.dir), Dir: f.dir, SeedPath: f.seed, PortBase: f.portBase,
		Reseed: f.reseed, Runner: sandbox.ExecRunner{Bin: bin}, Out: out,
	}, nil
}

func runSandbox(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, sandboxUsage)
		return 2
	}
	sub := args[0]
	fs := flag.NewFlagSet("obs sandbox "+sub, flag.ContinueOnError)
	fs.SetOutput(stderr)
	var f sandboxFlags
	f.register(fs)
	switch sub {
	case "up", "down", "status":
	case "help", "-h", "--help":
		fmt.Fprint(stdout, sandboxUsage)
		return 0
	default:
		fmt.Fprintf(stderr, "obs sandbox: unknown subcommand %q\n\n%s", sub, sandboxUsage)
		return 2
	}
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	switch sub {
	case "status":
		cfg := sandbox.Config{Project: projectName(f.project, f.dir)}
		info, err := sandbox.Status(ctx, cfg)
		if err != nil {
			fmt.Fprintf(stderr, "obs sandbox status: %v\n", err)
			return 1
		}
		printInfo(stdout, info)
		return 0
	}
	var cfg sandbox.Config
	var err error
	if sub == "down" {
		cfg, err = f.config(stdout)
	} else {
		cfg, err = f.upConfig(stdout)
	}
	if err != nil {
		fmt.Fprintf(stderr, "obs sandbox %s: %v\n", sub, err)
		return 2
	}
	if sub == "down" {
		if err := sandbox.Down(ctx, cfg); err != nil {
			fmt.Fprintf(stderr, "obs sandbox down: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "obs sandbox down: project %s removed (only containers, volumes and the network labelled %s=%s)\n", cfg.Project, sandbox.LabelKey, cfg.Project)
		return 0
	}
	info, err := sandbox.Up(ctx, cfg)
	if err != nil {
		fmt.Fprintf(stderr, "obs sandbox up: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "obs sandbox up: project %s is up\n", info.Project)
	printInfo(stdout, info)
	return 0
}

func printInfo(w io.Writer, i *sandbox.Info) {
	fmt.Fprintf(w, "  Grafana         %s  (anonymous viewer; admin/admin)\n", i.Grafana)
	names := make([]string, 0, len(i.URLs))
	for n := range i.URLs {
		if n != "grafana" {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(w, "  %-15s %s\n", n, i.URLs[n])
	}
	if i.From != 0 {
		fmt.Fprintf(w, "  seed window     from=%d to=%d (unix ms)\n", i.From, i.To)
	}
}
