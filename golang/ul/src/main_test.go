package main

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// fakeDhEnv makes the test binary act as a dh: `dh json <page...>` prints the JSON recorded
// beside each page (page.dhjson, made by tests/record-dh.sh with the real dh), or, for a page
// with a page.dherr file, the error the real dh prints for a page that does not parse, and
// exits 1. A test binary needs no interpreter, so this runs in the nix sandbox too.
const fakeDhEnv = "UL_TEST_FAKE_DH"

func TestMain(m *testing.M) {
	if os.Getenv(fakeDhEnv) == "1" {
		os.Exit(fakeDh(os.Args[1:]))
	}
	os.Exit(m.Run())
}

func fakeDh(args []string) int {
	if len(args) < 1 || args[0] != "json" {
		fmt.Fprintln(os.Stderr, "usage: dh json <page...>")
		return 2
	}
	for _, p := range args[1:] {
		if b, err := os.ReadFile(p + ".dherr"); err == nil {
			fmt.Fprint(os.Stderr, strings.TrimRight(string(b), "\n")+"\n")
			return 1
		}
		b, err := os.ReadFile(p + ".dhjson")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		os.Stdout.Write(b)
	}
	return 0
}
