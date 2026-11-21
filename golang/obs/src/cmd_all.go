package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"path/filepath"

	"github.com/AtomiCloud/obs/internal/contract"
)

// runAll reuses each command with its original options, changing only --dir.
// Success is one summary per contract; failures keep their detailed diagnostics.
func runAll(cmd string, fs *flag.FlagSet, dir string, stdout, stderr io.Writer, runner func([]string, io.Writer, io.Writer) int) int {
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
	paths, err := contract.DiscoverAll(dir)
	if err != nil {
		fmt.Fprintf(stderr, "%s --all: %v\n", cmd, err)
		return 2
	}
	if len(paths) == 0 {
		fmt.Fprintf(stderr, "%s --all: no contracts found from %s\n", cmd, dir)
		return 2
	}
	var folders []string
	for _, file := range paths {
		folders = append(folders, filepath.Dir(file))
	}
	if cmd == "obs check" || cmd == "obs test" {
		folders, _, err = contract.FoldersScopeSkip(dir, true)
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
	for _, folder := range folders {
		var out, errb bytes.Buffer
		selected := append(append([]string(nil), args...), "--dir="+folder)
		rc := runner(selected, &out, &errb)
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
			_, _ = stderr.Write(errb.Bytes())
		}
		label := contract.RelPath(folder) + "/"
		for _, file := range paths {
			if filepath.Dir(file) == folder {
				label = contract.RelPath(file)
				break
			}
		}
		fmt.Fprintf(stdout, "%s --all: %s: %s\n", cmd, label, status)
	}
	return result
}
