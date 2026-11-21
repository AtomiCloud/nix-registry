package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/AtomiCloud/obs/internal/contract"
	"github.com/AtomiCloud/obs/internal/files"
	"github.com/AtomiCloud/obs/internal/gen"
)

// runAll reuses each command with its original options, changing only --dir.
// Success is one summary per contract; failures keep their detailed diagnostics.
func runAll(cmd string, fs *flag.FlagSet, dir string, stdout, stderr io.Writer, runner func([]string, io.Writer, io.Writer, *contract.Scope) int) int {
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "%s --all: unexpected argument %q\n", cmd, fs.Arg(0))
		return 2
	}
	for _, name := range []string{"out", "steps", "recorded"} {
		if f := fs.Lookup(name); f != nil && f.Value.String() != "" {
			fmt.Fprintf(stderr, "%s --all: --%s would share one output file across contracts; run --dir separately for file exports\n", cmd, name)
			return 2
		}
	}
	start, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "%s --all: %v\n", cmd, err)
		return 2
	}
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "dir" {
			start = dir
		}
	})
	root := gen.GitRoot(start)
	if root == "" {
		root = start
	}
	scope, err := contract.NewScope(root)
	if err != nil {
		fmt.Fprintf(stderr, "%s --all: %v\n", cmd, err)
		return 2
	}
	paths, err := scope.Contracts()
	if err != nil {
		fmt.Fprintf(stderr, "%s --all: %v\n", cmd, err)
		return 2
	}
	if len(paths) == 0 && cmd != "obs gen" && cmd != "obs gen --check" {
		fmt.Fprintf(stderr, "%s --all: no contracts found from %s\n", cmd, dir)
		return 2
	}
	var folders []string
	for _, file := range paths {
		folders = append(folders, filepath.Dir(file))
	}
	if cmd == "obs check" || cmd == "obs test" {
		folders, err = scope.Folders()
		if err != nil {
			fmt.Fprintf(stderr, "%s --all: %v\n", cmd, err)
			return 2
		}
	}
	var args []string
	fs.Visit(func(f *flag.Flag) {
		if f.Name != "all" && f.Name != "dir" {
			args = append(args, "--"+f.Name+"="+f.Value.String())
		}
	})
	result := 0
	if cmd == "obs gen" || cmd == "obs gen --check" {
		orphans, err := gen.MissingOwners(scope, paths)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		checking := fs.Lookup("check").Value.String() == "true"
		for _, orphan := range orphans {
			if checking {
				fmt.Fprintf(stdout, "FAIL %s: [gen-orphan] generated file names missing contract %s; run obs gen --all\n", scope.Rel(orphan.File), orphan.Contract)
				result = 1
			} else {
				if err := os.Remove(orphan.File); err != nil {
					fmt.Fprintln(stderr, err)
					return 2
				}
				fmt.Fprintf(stdout, "deleted %s (missing contract %s)\n", scope.Rel(orphan.File), orphan.Contract)
			}
		}
	}
	if len(paths) == 0 {
		if result != 0 {
			return result
		}
		fmt.Fprintf(stderr, "%s --all: no contracts found from %s\n", cmd, dir)
		return 2
	}
	for _, folder := range folders {
		var out, errb bytes.Buffer
		selected := append(append([]string(nil), args...), "--dir="+folder)
		rc := runner(selected, &out, &errb, scope)
		status := "ok"
		if rc != 0 {
			status = "FAILED"
			if rc == 2 {
				status = "ERROR"
			}
			if rc > result {
				result = rc
			}
			_, _ = stdout.Write(out.Bytes())
		}
		_, _ = stderr.Write(errb.Bytes())
		label := scope.Rel(folder) + "/"
		for _, file := range paths {
			if filepath.Dir(file) == folder {
				label = scope.Rel(file)
				break
			}
		}
		fmt.Fprintf(stdout, "%s --all: %s: %s\n", cmd, label, status)
	}
	return result
}

func loadScopedFiles(dir string, scope *contract.Scope) (*files.Set, error) {
	if scope == nil {
		return files.LoadSkip(dir, contract.PayloadSkip(dir, nil))
	}
	return files.LoadScoped(dir, func(file string) bool { return scope.Skips(dir, file) }, scope.Root)
}
