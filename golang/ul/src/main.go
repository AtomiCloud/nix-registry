// ul: the ubiquitous-language tool. It loads every UL.yaml of a repo, resolves their references
// and serves the result as JSON, Markdown and lookups. Spec: design/registry-tools/ul.md.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

var version = "dev"

type globals struct {
	root   string
	format string
}

type env struct {
	cwd    string
	getenv func(string) string
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
}

const usageText = `ul: the ubiquitous-language tool

Usage: ul [--root DIR] [--format F] <command> [flags]

Commands:
  all                 the final UL (--format json|md, --details)
  lookup <name>       every match for a word, alias or id (--from PATH, --format text|json|md)
  check               every rule (--format text|json); exit 1 on problems
  gen                 write UL.md beside each UL.yaml (--check: exit 1 on drift)
  --version

Exit codes: 0 ok; 1 not found / refused / problems; 2 usage or IO error.
`

type usageErr struct{ msg string }

func (u usageErr) Error() string { return u.msg }

// quietErr is an error the flag package already printed.
type quietErr struct{ error }

func (q quietErr) Unwrap() error { return q.error }

type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(s string) error { *m = append(*m, s); return nil }

func addCommon(fs *flag.FlagSet, g *globals) {
	fs.StringVar(&g.root, "root", g.root, "repo root (default: git top level, else the current directory)")
	fs.StringVar(&g.format, "format", g.format, "output format")
}

// parseArgs parses flags that may come before or after the positional arguments.
func parseArgs(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, quietErr{err}
		}
		if fs.NArg() == 0 {
			return pos, nil
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

func newFlagSet(name string, e *env) *flag.FlagSet {
	fs := flag.NewFlagSet("ul "+name, flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	return fs
}

func run(args []string, e *env) int {
	g := &globals{}
	top := newFlagSet("", e)
	top.Usage = func() { fmt.Fprint(e.stderr, usageText) }
	addCommon(top, g)
	showVersion := top.Bool("version", false, "print the version")
	if err := top.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *showVersion {
		fmt.Fprintf(e.stdout, "ul %s\n", version)
		return 0
	}
	rest := top.Args()
	if len(rest) == 0 {
		fmt.Fprint(e.stderr, usageText)
		return 2
	}
	cmd, rest := rest[0], rest[1:]
	var err error
	var code int
	switch cmd {
	case "version":
		fmt.Fprintf(e.stdout, "ul %s\n", version)
		return 0
	case "help":
		fmt.Fprint(e.stdout, usageText)
		return 0
	case "all":
		code, err = cmdAll(g, rest, e)
	case "lookup":
		code, err = cmdLookup(g, rest, e)
	case "check":
		code, err = cmdCheck(g, rest, e)
	case "gen":
		code, err = cmdGen(g, rest, e)
	default:
		fmt.Fprintf(e.stderr, "ul: unknown command %q\n\n%s", cmd, usageText)
		return 2
	}
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		var u usageErr
		var q quietErr
		switch {
		case errors.As(err, &q):
		case errors.As(err, &u):
			fmt.Fprintf(e.stderr, "ul %s: %s\n", cmd, u.msg)
		default:
			fmt.Fprintf(e.stderr, "ul: %v\n", err)
		}
		return 2
	}
	return code
}

func main() {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ul: %v\n", err)
		os.Exit(2)
	}
	os.Exit(run(os.Args[1:], &env{cwd: cwd, getenv: os.Getenv, stdin: os.Stdin, stdout: os.Stdout, stderr: os.Stderr}))
}
