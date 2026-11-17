package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/AtomiCloud/obs/internal/symbols"
)

func runSymbols(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "obs symbols: want put or decode")
		return 2
	}
	fs := flag.NewFlagSet("obs symbols "+args[0], flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("store", ".obs/symbols", "symbol store directory")
	file := fs.String("file", "", "symbol file (put) or stack text file (decode)")
	kind := fs.String("kind", "native", "native, jit or dart")
	id := fs.String("build-id", "", "GNU build id, Dart build id, or module MVID")
	app := fs.String("app", "", "module name (no path)")
	obfuscation := fs.String("obfuscation-map", "", "Dart obfuscation map JSON")
	exe := fs.String("symbolizer", "llvm-symbolizer", "llvm-symbolizer executable")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if fs.NArg() != 0 || *file == "" {
		fmt.Fprintln(stderr, "obs symbols: --file is required; no positional arguments allowed")
		return 2
	}
	if args[0] != "put" && args[0] != "decode" {
		fmt.Fprintf(stderr, "obs symbols: unknown operation %q\n", args[0])
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	data, err := os.ReadFile(*file)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	store := symbols.Directory{Root: *root}
	if args[0] == "put" {
		var mapping []byte
		if *obfuscation != "" {
			mapping, err = os.ReadFile(*obfuscation)
			if err != nil {
				fmt.Fprintln(stderr, err)
				return 2
			}
		}
		key, e := symbols.Put(ctx, store, symbols.Ref{Kind: *kind, ID: *id, App: *app}, data, mapping)
		if e != nil {
			fmt.Fprintf(stderr, "obs symbols put %s: %v\n", *file, e)
			return 1
		}
		fmt.Fprintln(stdout, key)
		return 0
	}
	decoded, err := (symbols.Decoder{Store: store, Symbolizer: symbols.LLVM{Executable: *exe}}).Decode(ctx, string(data))
	if err != nil {
		fmt.Fprintf(stderr, "obs symbols decode %s: %v\n", *file, err)
		return 1
	}
	if _, err = io.WriteString(stdout, decoded); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}
