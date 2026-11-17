package gen_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AtomiCloud/obs/internal/contract"
	"github.com/AtomiCloud/obs/internal/gen"
)

func TestCheckOrphansAfterDroppingATarget(t *testing.T) {
	c, err := contract.Parse("contract.yaml", []byte("service: s\ngenerate: [{lang: go, out: custom/go}, {lang: dart, out: old/dart}]\n"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	outs := []gen.Output{
		{Path: "custom/go/signals.go", Content: []byte(gen.Header(c, "// ") + "package signals\n"), Lang: "go"},
		{Path: "old/dart/signals.g.dart", Content: []byte(gen.Header(c, "// ")), Lang: "dart"},
	}
	if err := gen.Write(root, outs); err != nil {
		t.Fatal(err)
	}
	if probs, err := gen.Check(root, outs, c); err != nil || len(probs) != 0 {
		t.Fatalf("baseline: %v %v", probs, err)
	}
	// The old target has disappeared completely; its directory is not in c.Generate.
	c.Generate = c.Generate[:1]
	probs, err := gen.Check(root, outs[:1], c)
	if err != nil || len(probs) != 1 || !strings.Contains(probs[0], "old/dart/signals.g.dart: [gen-orphan]") {
		t.Fatalf("dropped target must fail naming its file: %v %v", probs, err)
	}
	// Hand-written files and symlinks are never treated as generated files.
	if err := os.Remove(filepath.Join(root, "old/dart/signals.g.dart")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "old/dart/notes.dart"), []byte("// hand written\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if probs, err := gen.Check(root, outs[:1], c); err != nil || len(probs) != 0 {
		t.Fatalf("restored: %v %v", probs, err)
	}
}

func TestCheckOrphansWithinSelectedTarget(t *testing.T) {
	c := &contract.Contract{Hash: strings.Repeat("0", 64), Generate: []contract.Target{{Lang: "go", Out: "custom/go"}, {Lang: "dart", Out: "custom/dart"}}}
	root := t.TempDir()
	goOut := gen.Output{Path: "custom/go/signals.go", Content: []byte(gen.Header(c, "// ")), Lang: "go"}
	other := gen.Output{Path: "custom/dart/signals.g.dart", Content: []byte(gen.Header(c, "// ")), Lang: "dart"}
	if err := gen.Write(root, []gen.Output{goOut, other}); err != nil {
		t.Fatal(err)
	}
	if probs, err := gen.Check(root, []gen.Output{goOut}, c, "go"); err != nil || len(probs) != 0 {
		t.Fatalf("unselected target: %v %v", probs, err)
	}
	orphan := gen.Output{Path: "custom/go/old.go", Content: []byte(gen.Header(c, "// ")), Lang: "go"}
	if err := gen.Write(root, []gen.Output{orphan}); err != nil {
		t.Fatal(err)
	}
	probs, err := gen.Check(root, []gen.Output{goOut}, c, "go")
	if err != nil || len(probs) != 1 || !strings.Contains(probs[0], "custom/go/old.go: [gen-orphan]") {
		t.Fatalf("extra file: %v %v", probs, err)
	}
}

func TestCheckOrphansBelongToOneContract(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	load := func(service string) *contract.Contract {
		t.Helper()
		file := filepath.Join(root, "services", service, "observability", "contract.yaml")
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("service: "+service+"\ngenerate: [{lang: go, out: generated}]\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		c, err := contract.LoadValid(file)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	a, b := load("a"), load("b")
	if gen.ContractPath(a) != "services/a/observability/contract.yaml" || gen.ContractPath(b) != "services/b/observability/contract.yaml" {
		t.Fatal("headers must name distinct repository-relative source paths")
	}
	outs := []gen.Output{
		{Path: "generated/a.go", Lang: "go", Content: []byte(gen.Header(a, "// ") + "package a\n")},
		{Path: "generated/b.go", Lang: "go", Content: []byte(gen.Header(b, "// ") + "package b\n")},
	}
	if err := gen.Write(root, outs); err != nil {
		t.Fatal(err)
	}
	if probs, err := gen.Check(root, outs[:1], a); err != nil || len(probs) != 0 {
		t.Fatalf("contract B must be ignored: %v %v", probs, err)
	}
	a.Generate = nil // only A's target is removed
	probs, err := gen.Check(root, nil, a)
	if err != nil || len(probs) != 1 || !strings.Contains(probs[0], "generated/a.go: [gen-orphan]") {
		t.Fatalf("only A's removed output must be flagged: %v %v", probs, err)
	}
	// Invoking from a service folder still uses the same repository-relative identity.
	t.Chdir(filepath.Join(root, "services", "a"))
	relative, err := contract.Load("observability/contract.yaml")
	if err != nil || gen.ContractPath(relative) != gen.ContractPath(a) {
		t.Fatalf("service cwd changed source identity: %v", err)
	}
}

func TestCheckExplicitLanguageWithOnlyOneRemainingTarget(t *testing.T) {
	c := &contract.Contract{Hash: strings.Repeat("0", 64), Generate: []contract.Target{{Lang: "go", Out: "generated/go"}}}
	root := t.TempDir()
	goOut := gen.Output{Path: "generated/go/signals.go", Lang: "go", Content: []byte(gen.Header(c, "// "))}
	oldBun := gen.Output{Path: "generated/bun/signals.ts", Lang: "bun", Content: []byte(gen.Header(c, "// "))}
	if err := gen.Write(root, []gen.Output{goOut, oldBun}); err != nil {
		t.Fatal(err)
	}
	if probs, err := gen.Check(root, []gen.Output{goOut}, c, "go"); err != nil || len(probs) != 0 {
		t.Fatalf("explicit --lang go must ignore dropped Bun target: %v %v", probs, err)
	}
	if probs, err := gen.Check(root, []gen.Output{goOut}, c); err != nil || len(probs) != 1 || !strings.Contains(probs[0], "generated/bun/signals.ts: [gen-orphan]") {
		t.Fatalf("unfiltered check must find dropped Bun target: %v %v", probs, err)
	}
}
