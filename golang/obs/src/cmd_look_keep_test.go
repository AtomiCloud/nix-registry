package main

import (
	"strings"
	"testing"
)

func TestLookKeepFlagPreservesNonblockingSetup(t *testing.T) {
	dir, cl := lookProject(t)
	t.Setenv("OBS_CONTAINER_RUNTIME", "nerdctl")
	rc, out := obs("look", "--keep", "--json", "--dir", dir, "--checklist", cl, "--file", "dashboards/overview.json")
	if rc != 0 || strings.Contains(out, "flag provided but not defined") || !strings.Contains(out, `"keep": true`) || !strings.Contains(out, "sandbox setup failed") {
		t.Fatalf("rc=%d %s", rc, out)
	}
}
