package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestProfileBaselineRequiresOwn(t *testing.T) {
	var out, stderr bytes.Buffer
	rc := runProfileBaseline([]string{"--format", "pprof", "--service", "s", "--scenario", "n", "a.pb", "b.pb", "c.pb"}, &out, &stderr)
	if rc != 2 || !strings.Contains(stderr.String(), "--own") {
		t.Fatalf("want exit 2 naming --own before reading profiles: rc %d: %s", rc, stderr.String())
	}
}
