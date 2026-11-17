package main

import (
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/AtomiCloud/obs/internal/contract"
	"github.com/AtomiCloud/obs/internal/gen"
	_ "github.com/AtomiCloud/obs/internal/gen/all"
)

// loadContract reads and validates <dir>/contract.yaml, printing every issue. ok is false when
// the contract cannot be used (then the exit code is the caller's rc).
func loadContract(dir, cmd string, stdout, stderr io.Writer) (c *contract.Contract, rc int) {
	path := filepath.Join(dir, "contract.yaml")
	c, err := contract.Load(path)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", cmd, err)
		return nil, 2
	}
	if is := c.Validate(); len(is) > 0 {
		for _, i := range is {
			fmt.Fprintf(stdout, "FAIL %s %s\n", path, i)
		}
		fmt.Fprintf(stdout, "%s: FAILED: %d contract failures in %s\n", cmd, len(is), path)
		return nil, 1
	}
	return c, 0
}

// runGen is `obs gen` and `obs gen --check` (concept section 3.4): types and constants per
// language from the contract, contract/events.json and the aggregation file (section 4.1).
func runGen(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("obs gen", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", "observability", "the observability folder (holds contract.yaml)")
	check := fs.Bool("check", false, "regenerate into a temp dir and diff with the committed files; fail on a hand edit or a stale generate")
	langs := fs.String("lang", "", "comma-separated languages to generate (default: every `generate:` target of the contract)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cmd := "obs gen"
	if *check {
		cmd = "obs gen --check"
	}
	c, rc := loadContract(*dir, cmd, stdout, stderr)
	if c == nil {
		return rc
	}
	var want []string
	if *langs != "" {
		for _, l := range strings.Split(*langs, ",") {
			l = strings.TrimSpace(l)
			if _, ok := gen.Get(l); !ok {
				fmt.Fprintf(stderr, "%s: no generator for %q (have %s)\n", cmd, l, strings.Join(gen.Langs(), ", "))
				return 2
			}
			want = append(want, l)
		}
	}
	// A `generate:` target with no generator in this build is an input error, not a silent skip.
	for _, t := range c.Generate {
		if _, ok := gen.Get(t.Lang); !ok && (len(want) == 0 || contains(want, t.Lang)) {
			fmt.Fprintf(stderr, "%s: contract generate: language %q has no generator in this build (have %s)\n", cmd, t.Lang, strings.Join(gen.Langs(), ", "))
			return 2
		}
	}
	outs, err := gen.Plan(c, *dir, want)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", cmd, err)
		return 2
	}
	root := gen.Root(*dir)
	if *check {
		probs, err := gen.Check(root, outs, c)
		if err != nil {
			fmt.Fprintf(stderr, "%s: %v\n", cmd, err)
			return 2
		}
		for _, p := range probs {
			fmt.Fprintf(stdout, "FAIL %s\n", p)
		}
		if len(probs) > 0 {
			fmt.Fprintf(stdout, "%s: FAILED: %d of %d files stale, edited or missing (contract hash %s)\n", cmd, len(probs), len(outs), gen.ShortHash(c.Hash))
			return 1
		}
		fmt.Fprintf(stdout, "%s: ok: %d files equal a fresh generate (contract hash %s)\n", cmd, len(outs), gen.ShortHash(c.Hash))
		return 0
	}
	if err := gen.Write(root, outs); err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", cmd, err)
		return 2
	}
	for _, o := range outs {
		fmt.Fprintf(stdout, "wrote %s\n", o.Path)
	}
	fmt.Fprintf(stdout, "%s: ok: %d files written (contract hash %s)\n", cmd, len(outs), gen.ShortHash(c.Hash))
	return 0
}

func contains(a []string, s string) bool {
	for _, x := range a {
		if x == s {
			return true
		}
	}
	return false
}
