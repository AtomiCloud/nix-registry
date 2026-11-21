package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAllDefaultDoesNotSelectNestedObservabilityRepo(t *testing.T) {
	root := repo(t, map[string]string{
		"feature/observability/contract.yaml": "service: checkout\n",
		"feature/observability/seed.yaml":     seedDoc,
		"observability/.git/HEAD":             "ref: refs/heads/main\n",
		"observability/contract.yaml":         "service: nested\nmetrics: {nested.requests: {kind: invalid}}\n",
		"observability/seed.yaml":             seedDoc,
	})
	t.Chdir(root)
	for _, cmd := range []string{"gen", "check", "test"} {
		if rc, output := obs(cmd, "--all"); rc != 0 || strings.Count(output, "--all:") != 1 || !strings.Contains(output, "feature/observability/contract.yaml") {
			t.Errorf("%s default must start at cwd and prune nested repo: rc=%d %s", cmd, rc, output)
		}
	}
	if rc, output := obs("check", "--all", "--dir", "observability"); rc == 0 || !strings.Contains(output, "invalid") {
		t.Errorf("explicit --dir must select nested repository: rc=%d %s", rc, output)
	}
}

func TestAllObsYamlInheritanceSurvivesExport(t *testing.T) {
	root := repo(t, map[string]string{
		"observability/obs.yaml":             "versions: {victoriametrics: v1.157.0}\n",
		"observability/friend/contract.yaml": "service: checkout\n",
		"observability/friend/seed.yaml":     seedDoc,
	})
	t.Chdir(root)
	for _, git := range []bool{true, false} {
		if !git {
			if err := os.RemoveAll(filepath.Join(root, ".git")); err != nil {
				t.Fatal(err)
			}
		}
		for _, cmd := range []string{"check", "test"} {
			if rc, output := obs(cmd, "--all"); rc != 1 || !strings.Contains(output, "1.157.0") || !strings.Contains(output, "observability/obs.yaml") {
				t.Errorf("git=%v %s must retain inherited version gate: rc=%d %s", git, cmd, rc, output)
			}
		}
	}
	if rc, output := obs("check", "--dir", filepath.Join(root, "observability/friend")); rc != 0 {
		t.Errorf("single-folder export lookup must stay unchanged: rc=%d %s", rc, output)
	}
}

func TestAllStandalonePrunesDocsPayloads(t *testing.T) {
	root := repo(t, map[string]string{
		"contract.yaml":                       "service: checkout\nlogs: {Failed: {event: root.failed, level: error}}\n",
		"seed.yaml":                           seedDoc,
		"dashboards/root.json":                `{"title":"Root","schemaVersion":42,"panels":[]}`,
		"friend/contract.yaml":                "service: checkout\n",
		"friend/seed.yaml":                    seedDoc,
		"docs/dashboards/broken.json":         `{"title":`,
		"examples/alerts/broken/warning.yaml": "apiVersion: [\n",
	})
	t.Chdir(root)
	for _, git := range []bool{true, false} {
		if !git {
			if err := os.RemoveAll(filepath.Join(root, ".git")); err != nil {
				t.Fatal(err)
			}
		}
		for _, cmd := range []string{"check", "test"} {
			if rc, output := obs(cmd, "--all"); rc != 0 || strings.Count(output, "--all:") != 2 || strings.Contains(output, "docs/") || strings.Contains(output, "examples/") || !strings.Contains(output, "contract.yaml") {
				t.Errorf("git=%v %s must keep root/contract folders and skip docs/examples: rc=%d %s", git, cmd, rc, output)
			}
		}
	}
	gen2Write(t, root, "docs/observability/dashboards/broken.json", `{"title":`)
	if rc, output := obs("check", "--all"); rc == 0 || !strings.Contains(output, "docs/observability/") {
		t.Errorf("an explicit observability path still qualifies: rc=%d %s", rc, output)
	}
}

func TestAllExplicitExportDirFromOtherWorkingDirectories(t *testing.T) {
	parent := t.TempDir()
	export := filepath.Join(parent, "ex")
	gen2Write(t, export, "go.mod", "module example.com/export\ngo 1.27.4\n")
	for _, feature := range []string{"one", "two"} {
		gen2Write(t, export, "observability/"+feature+"/contract.yaml", "service: shop\nlogs: {Failed: {event: "+feature+".failed, level: error}}\ngenerate: [{lang: go, out: gen/"+feature+"}]\n")
	}
	if err := os.Mkdir(filepath.Join(export, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(export)
	if rc, output := obs("gen", "--all"); rc != 0 {
		t.Fatal(output)
	}
	if err := os.RemoveAll(filepath.Join(export, ".git")); err != nil {
		t.Fatal(err)
	}
	unrelated := t.TempDir()
	for _, tc := range []struct{ cwd, dir string }{{parent, "ex"}, {unrelated, export}} {
		t.Chdir(tc.cwd)
		if rc, output := obs("gen", "--check", "--all", "--dir", tc.dir); rc != 0 || strings.Count(output, "--all:") != 2 || !strings.Contains(output, "observability/one/contract.yaml") || !strings.Contains(output, "observability/two/contract.yaml") {
			t.Errorf("cwd=%s --dir=%s: rc=%d %s", tc.cwd, tc.dir, rc, output)
		}
	}
}
