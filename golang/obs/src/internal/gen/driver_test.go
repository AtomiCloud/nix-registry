package gen_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AtomiCloud/obs/internal/contract"
	"github.com/AtomiCloud/obs/internal/gen"
	_ "github.com/AtomiCloud/obs/internal/gen/all"
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

// writeProject makes <root>/<svc>/observability/contract.yaml with one bun target and returns the loaded contract.
func writeProject(t *testing.T, root, svc, out string) (*contract.Contract, string) {
	t.Helper()
	obsDir := filepath.Join(root, svc, "observability")
	if err := os.MkdirAll(obsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "service: s\nlogs:\n  A: {event: s.a, level: info}\ngenerate: [{lang: bun, out: " + out + "}]\n"
	if err := os.WriteFile(filepath.Join(obsDir, "contract.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := contract.LoadValid(filepath.Join(obsDir, "contract.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return c, obsDir
}

// Review item 4: an out: folder outside the project folder but inside the git repository is a valid
// target; when it is dropped, its old file is an orphan the walk must find (the walk starts at the git root).
func TestCheckOrphanOutsideProjectInsideGit(t *testing.T) {
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	c, obsDir := writeProject(t, repo, "svc", "../shared/bun")
	outs, err := gen.Plan(c, obsDir, nil)
	if err != nil {
		t.Fatalf("an out: inside the git root must be accepted: %v", err)
	}
	root := gen.Root(obsDir)
	if err := gen.Write(root, outs); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repo, "shared", "bun", "signals.ts")); err != nil {
		t.Fatalf("generated file not under the git root: %v", err)
	}
	if probs, err := gen.Check(root, outs, c); err != nil || len(probs) != 0 {
		t.Fatalf("baseline: %v %v", probs, err)
	}
	c.Generate = nil // the target is dropped; its file stays behind outside the project folder
	plan, err := gen.Plan(c, obsDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	probs, err := gen.Check(root, plan, c)
	if err != nil || len(probs) != 1 || !strings.Contains(probs[0], "../shared/bun/signals.ts: [gen-orphan]") {
		t.Fatalf("the dropped target outside the project folder must be an orphan: %v %v", probs, err)
	}
}

// Review item 4: out: may not leave the git repository; with no repository it may not use '..'.
func TestPlanRefusesOutEscapingRoot(t *testing.T) {
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	c, obsDir := writeProject(t, repo, "svc", "../../elsewhere/bun")
	if _, err := gen.Plan(c, obsDir, nil); err == nil || !strings.Contains(err.Error(), `out: "../../elsewhere/bun" leaves the git repository`) {
		t.Fatalf("an out: outside the git root must be refused naming it: %v", err)
	}
	c.Generate[0].Out = "/abs/bun"
	if _, err := gen.Plan(c, obsDir, nil); err == nil || !strings.Contains(err.Error(), `out: "/abs/bun" must be relative`) {
		t.Fatalf("an absolute out: must be refused: %v", err)
	}
	plain := t.TempDir() // no .git anywhere above a temp dir
	c2, obsDir2 := writeProject(t, plain, "p", "../bun")
	if _, err := gen.Plan(c2, obsDir2, nil); err == nil || !strings.Contains(err.Error(), `out: "../bun" leaves the project folder`) {
		t.Fatalf("outside git, '..' in out: must be refused: %v", err)
	}
	c2.Generate[0].Out = "gen/../bun" // cleans to bun: stays inside
	if _, err := gen.Plan(c2, obsDir2, nil); err != nil {
		t.Fatalf("an out: that stays inside must be accepted: %v", err)
	}
}

// Review item 5: with no .git, the header's contract path is relative to the folder holding observability/,
// whatever the working directory, so --check from another folder sees the same headers.
func TestContractPathWithoutGitIgnoresCwd(t *testing.T) {
	parent := t.TempDir()
	c, _ := writeProject(t, parent, "p", "gen/bun")
	if got := gen.ContractPath(c); got != "observability/contract.yaml" {
		t.Fatalf("absolute load: %q", got)
	}
	t.Chdir(parent)
	rel, err := contract.Load(filepath.Join("p", "observability", "contract.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if got := gen.ContractPath(rel); got != "observability/contract.yaml" {
		t.Fatalf("load from the parent folder gave %q; the header would change with the cwd", got)
	}
	t.Chdir(filepath.Join(parent, "p"))
	here, err := contract.Load(filepath.Join("observability", "contract.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if got := gen.ContractPath(here); got != "observability/contract.yaml" {
		t.Fatalf("load from the project folder gave %q", got)
	}
}

// Review round 5 (low): a symlink inside the project that points outside the repository must not let out:
// escape; out: is checked on its resolved path.
func TestPlanRefusesOutThroughSymlink(t *testing.T) {
	outside := t.TempDir()
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	c, obsDir := writeProject(t, repo, "svc", "ext/bun")
	if err := os.Symlink(outside, filepath.Join(repo, "svc", "ext")); err != nil {
		t.Fatal(err)
	}
	if _, err := gen.Plan(c, obsDir, nil); err == nil || !strings.Contains(err.Error(), `out: "ext/bun" leaves the git repository`) {
		t.Fatalf("out: through a symlink to outside the git root must be refused: %v", err)
	}
	plain := t.TempDir()
	c2, obsDir2 := writeProject(t, plain, "p", "ext/bun")
	if err := os.Symlink(outside, filepath.Join(plain, "p", "ext")); err != nil {
		t.Fatal(err)
	}
	if _, err := gen.Plan(c2, obsDir2, nil); err == nil || !strings.Contains(err.Error(), `out: "ext/bun" leaves the project folder`) {
		t.Fatalf("outside git, out: through a symlink to outside the project must be refused: %v", err)
	}
	inside := filepath.Join(repo, "svc", "real")
	if err := os.Mkdir(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(inside, filepath.Join(repo, "svc", "alias")); err != nil {
		t.Fatal(err)
	}
	c.Generate[0].Out = "alias/bun"
	if _, err := gen.Plan(c, obsDir, nil); err != nil {
		t.Fatalf("a symlink that stays inside the repository must be accepted: %v", err)
	}
}
