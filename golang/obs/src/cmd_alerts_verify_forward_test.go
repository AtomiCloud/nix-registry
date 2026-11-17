package main

import (
	"flag"
	"testing"
)

func TestPruneForwardsAllParsedSettings(t *testing.T) {
	makeFlags := func() (*flag.FlagSet, *string, *bool, *int) {
		f := flag.NewFlagSet("fixture", flag.ContinueOnError)
		return f, f.String("vl-settings", "default", ""), f.Bool("enabled", false, ""), f.Int("workers", 0, "")
	}
	original, _, _, _ := makeFlags()
	if err := original.Parse([]string{"--vl-settings=--literal-path", "--enabled", "--workers=3"}); err != nil {
		t.Fatal(err)
	}
	child, setting, enabled, workers := makeFlags()
	if err := child.Parse(pruneForwardedFlags(original)); err != nil {
		t.Fatal(err)
	}
	if *setting != "--literal-path" || !*enabled || *workers != 3 {
		t.Fatalf("child lost settings: %s %v %d", *setting, *enabled, *workers)
	}
}
