package obsyaml

import (
	"os"
	"path/filepath"
	"testing"
)

func put(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A feature folder with no obs.yaml reads the nearest enclosing observability folder's; its own wins; none is nil.
func TestLoadFallsBackToEnclosingObservability(t *testing.T) {
	root := t.TempDir()
	put(t, filepath.Join(root, ".git", "HEAD"), "ref")
	put(t, filepath.Join(root, "observability", "obs.yaml"), matching)
	feature := filepath.Join(root, "observability", "friend")
	deep := filepath.Join(feature, "sub")
	for _, d := range []string{feature, deep} {
		put(t, filepath.Join(d, "contract.yaml"), "service: s\n")
		f, err := Load(d)
		if err != nil || f == nil {
			t.Fatalf("Load(%s) = %v, %v: want the enclosing obs.yaml", d, f, err)
		}
		if !f.Inherited || f.Path != filepath.Join(root, "observability", "obs.yaml") {
			t.Fatalf("Load(%s): Inherited=%v Path=%s", d, f.Inherited, f.Path)
		}
	}
	put(t, filepath.Join(feature, "obs.yaml"), "versions:\n  tempo: 3.1.1\n")
	f, err := Load(feature)
	if err != nil || f == nil || f.Inherited || f.Path != filepath.Join(feature, "obs.yaml") {
		t.Fatalf("own obs.yaml must win: %+v %v", f, err)
	}
	// the nearest wins over a farther one
	put(t, filepath.Join(deep, "observability", "obs.yaml"), "versions:\n  tempo: 3.1.1\n")
	g := filepath.Join(deep, "observability", "x")
	put(t, filepath.Join(g, "contract.yaml"), "service: s\n")
	if f, err := Load(g); err != nil || f == nil || f.Path != filepath.Join(deep, "observability", "obs.yaml") {
		t.Fatalf("nearest enclosing: %+v %v", f, err)
	}
	// a feature outside any observability folder with an obs.yaml, or a parent not named observability: none
	other := filepath.Join(root, "App", "Friend", "observability")
	put(t, filepath.Join(other, "contract.yaml"), "service: s\n")
	if f, err := Load(other); err != nil || f != nil {
		t.Fatalf("no enclosing observability folder: %+v %v", f, err)
	}
	notNamed := filepath.Join(root, "config")
	put(t, filepath.Join(notNamed, "obs.yaml"), matching)
	put(t, filepath.Join(notNamed, "friend", "contract.yaml"), "service: s\n")
	if f, err := Load(filepath.Join(notNamed, "friend")); err != nil || f != nil {
		t.Fatalf("a parent not named observability is not read: %+v %v", f, err)
	}
}

// The search stops at the Git root: an observability folder above the repository is not this repository's.
func TestEnclosingStopsAtGitRoot(t *testing.T) {
	outer := t.TempDir()
	put(t, filepath.Join(outer, "observability", "obs.yaml"), matching)
	repo := filepath.Join(outer, "observability", "repo")
	put(t, filepath.Join(repo, ".git", "HEAD"), "ref")
	d := filepath.Join(repo, "feature")
	put(t, filepath.Join(d, "contract.yaml"), "service: s\n")
	if f, err := Load(d); err != nil || f != nil {
		t.Fatalf("an obs.yaml above the Git root was read: %+v %v", f, err)
	}
}

// Only the repository is searched: a repository nested under a folder named observability does not read that
// folder's obs.yaml, from its root or from below it, and outside any Git repository nothing is read.
func TestEnclosingNeverLeavesTheRepository(t *testing.T) {
	outer := t.TempDir()
	put(t, filepath.Join(outer, "observability", "obs.yaml"), matching)
	repo := filepath.Join(outer, "observability", "repo")
	put(t, filepath.Join(repo, ".git", "HEAD"), "ref")
	put(t, filepath.Join(repo, "contract.yaml"), "service: s\n")
	put(t, filepath.Join(repo, "friend", "contract.yaml"), "service: s\n")
	for _, d := range []string{repo, filepath.Join(repo, "friend")} {
		if f, err := Load(d); err != nil || f != nil {
			t.Fatalf("Load(%s) read an obs.yaml outside the repository: %+v %v", d, f, err)
		}
	}
	// a .git that is a file (a worktree) is a Git root too
	wt := filepath.Join(outer, "observability", "wt")
	put(t, filepath.Join(wt, ".git"), "gitdir: elsewhere")
	if f, err := Load(wt); err != nil || f != nil {
		t.Fatalf("a worktree root read an obs.yaml outside it: %+v %v", f, err)
	}
	// no repository at all: no folder above is this repository's
	bare := t.TempDir()
	put(t, filepath.Join(bare, "observability", "obs.yaml"), matching)
	put(t, filepath.Join(bare, "observability", "friend", "contract.yaml"), "service: s\n")
	if f, err := Load(filepath.Join(bare, "observability", "friend")); err != nil || f != nil {
		t.Fatalf("outside a Git repository: %+v %v", f, err)
	}
	// the Git root itself named observability: its obs.yaml is the repository's, for a feature below it
	root := filepath.Join(t.TempDir(), "observability")
	put(t, filepath.Join(root, ".git", "HEAD"), "ref")
	put(t, filepath.Join(root, "obs.yaml"), matching)
	put(t, filepath.Join(root, "friend", "contract.yaml"), "service: s\n")
	if f, err := Load(filepath.Join(root, "friend")); err != nil || f == nil || !f.Inherited || f.Path != filepath.Join(root, "obs.yaml") {
		t.Fatalf("a root named observability: %+v %v", f, err)
	}
}
