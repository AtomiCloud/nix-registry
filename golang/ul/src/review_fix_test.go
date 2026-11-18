package main

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// failWriter is a stdout or stderr that always fails, like a closed pipe or a full disk.
type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func runWith(cwd string, stdout, stderr io.Writer, args ...string) int {
	return run(args, &env{cwd: cwd, getenv: func(string) string { return "" },
		stdin: strings.NewReader(""), stdout: stdout, stderr: stderr})
}

// A failed write to stdout or stderr is an I/O error: exit 2, never a success with output cut off.
func TestOutputWriteErrorsExit2(t *testing.T) {
	clean := fixture(t, "clean")
	bad := fixture(t, "bad/UL201-see-unresolved")
	// each gen case gets its own folder with a missing UL.md: a gen that writes it fixes the drift
	drifted := func() string {
		d := fixture(t, "clean")
		if err := os.Remove(filepath.Join(d, "recipes/bread/UL.md")); err != nil {
			t.Fatal(err)
		}
		return d
	}
	for _, c := range []struct {
		name   string
		dir    string
		args   []string
		stdout bool // which stream fails: stdout when true, stderr when false
	}{
		{"all json", clean, []string{"all"}, true},
		{"all md", clean, []string{"all", "--format", "md"}, true},
		{"lookup json", clean, []string{"lookup", "oven", "--format", "json"}, true},
		{"lookup text", clean, []string{"lookup", "oven"}, true},
		{"lookup md", clean, []string{"lookup", "oven", "--format", "md"}, true},
		{"lookup no match", clean, []string{"lookup", "zzqqzz"}, false},
		{"lookup no match, near", clean, []string{"lookup", "ovn"}, false},
		{"check text", bad, []string{"check"}, true},
		{"check json", bad, []string{"check", "--format", "json"}, true},
		{"gen refuses on problems", bad, []string{"gen"}, false},
		{"gen wrote line", drifted(), []string{"gen"}, true},
		{"gen --check drift line", drifted(), []string{"gen", "--check"}, true},
	} {
		var out, errb io.Writer = io.Discard, io.Discard
		if c.stdout {
			out = failWriter{}
		} else {
			errb = failWriter{}
		}
		if code := runWith(c.dir, out, errb, append(c.args, "--root", c.dir)...); code != 2 {
			t.Errorf("%s: exit %d with a failing writer, want 2", c.name, code)
		}
	}
}

// ul gen never reads or writes through a symlinked UL.md: the target may be any file.
func TestGenRefusesSymlinkedUL(t *testing.T) {
	base := t.TempDir()
	repo := filepath.Join(base, "repo")
	writeTree(t, repo, map[string]string{"UL.yaml": ulYAML("ex.org/a", "Ay")})
	outside := filepath.Join(base, "outside.txt")
	if err := os.WriteFile(outside, []byte("precious\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../outside.txt", filepath.Join(repo, "UL.md")); err != nil {
		t.Skip("no symlinks here")
	}
	te := newEnv(t, repo)
	code, _, errs := te.ul("gen")
	if code != 1 || !strings.Contains(errs, "UL.md") || !strings.Contains(errs, "symlink") {
		t.Fatalf("gen: exit %d, stderr %q; want 1 naming the symlinked UL.md", code, errs)
	}
	if got, _ := os.ReadFile(outside); string(got) != "precious\n" {
		t.Fatalf("gen wrote through the symlink: %q", got)
	}
	for _, args := range [][]string{{"gen", "--check"}, {"check"}} {
		code, out, _ := te.ul(args...)
		if code != 1 || !strings.Contains(out, "UL.md:1: UL301 UL.md is a symlink") {
			t.Fatalf("ul %v: exit %d, stdout %q; want UL301 UL.md is a symlink", args, code, out)
		}
	}
	// A dangling link must not make gen create its target either.
	if err := os.Remove(outside); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := te.ul("gen"); code != 1 {
		t.Fatalf("dangling link: gen exit %d, want 1", code)
	}
	if _, err := os.Lstat(outside); err == nil {
		t.Fatal("gen created the target of a dangling UL.md link")
	}
	if st, _ := os.Lstat(filepath.Join(repo, "UL.md")); st == nil || st.Mode()&os.ModeSymlink == 0 {
		t.Fatal("gen replaced the symlink")
	}
}

// --from FILE where FILE is a symlink ranks by the folder of the file it points to.
func TestFromSymlinkedFile(t *testing.T) {
	repo := t.TempDir()
	writeTree(t, repo, map[string]string{
		"a/UL.yaml": ulYAML("ex.org/a", "Item"),
		"b/UL.yaml": ulYAML("ex.org/b", "Item"),
		"b/x.go":    "package x\n",
	})
	if err := os.Symlink("../b/x.go", filepath.Join(repo, "a", "link.go")); err != nil {
		t.Skip("no symlinks here")
	}
	want := []string{"ex.org/b#Item", "ex.org/a#Item"}
	for _, from := range []string{"a/link.go", filepath.Join(repo, "a", "link.go"), "b/x.go"} {
		if got := newEnv(t, repo).lookupIDs("Item", "--from", from); !slices.Equal(got, want) {
			t.Errorf("--from %s: %v, want %v", from, got, want)
		}
	}
	// A missing file still ranks by its nearest existing parent.
	if got := newEnv(t, repo).lookupIDs("Item", "--from", "a/missing.go"); !slices.Equal(got, []string{"ex.org/a#Item", "ex.org/b#Item"}) {
		t.Errorf("--from a/missing.go: %v", got)
	}
}

// `--` ends flag parsing: everything after it is a positional argument.
func TestDoubleDashEndsFlags(t *testing.T) {
	dir := fixture(t, "clean")
	te := newEnv(t, dir)
	for _, args := range [][]string{
		{"lookup", "--", "oven", "--format", "json"},
		{"lookup", "oven", "--", "--format", "json"},
		{"lookup", "--", "oven", "x"},
		{"all", "--", "--details"},
	} {
		if code, _, errs := te.ul(append(args, "--root", dir)...); code != 2 || !strings.Contains(errs, "usage") && !strings.Contains(errs, "unexpected") {
			t.Errorf("ul %v: exit %d, stderr %q; want a usage refusal (exit 2)", args, code, errs)
		}
	}
	// A name that looks like a flag is fine after `--`; flags before `--` and a lone word still work.
	if code, _, errs := te.ul("lookup", "--root", dir, "--", "-nope"); code != 1 || strings.Contains(errs, "flag provided") {
		t.Errorf("lookup -- -nope: exit %d, stderr %q; want 1 (not found)", code, errs)
	}
	if got := te.mustOK("lookup", "--format", "json", "--root", dir, "--", "oven"); !strings.Contains(got, `"query": "oven"`) {
		t.Errorf("flags before --: %s", got)
	}
	if got := te.mustOK("lookup", "oven", "--format", "json", "--root", dir); !strings.Contains(got, `"query": "oven"`) {
		t.Errorf("flags after the word: %s", got)
	}
	// A flag whose value is "--" does not end flag parsing.
	if code, _, _ := te.ul("lookup", "--from", "--", "oven", "--root", dir); code != 0 {
		t.Errorf("--from -- oven: exit %d, want 0", code)
	}
}

// ghAnchors computes the anchors GitHub gives the headings of md: lower-cased text without
// punctuation, spaces to hyphens, a "-1", "-2" ... suffix for repeats, fenced code skipped.
func ghAnchors(md string) (texts, anchors []string) {
	seen := map[string]int{}
	fenced := false
	md = regexp.MustCompile("(?m)^([^\\n#|`][^\\n]*)\\n(?:=+|-+)$").ReplaceAllString(md, "## $1") // setext to ATX
	for _, line := range strings.Split(md, "\n") {
		if strings.HasPrefix(line, "```") {
			fenced = !fenced
			continue
		}
		m := regexp.MustCompile(`^#{1,6} (.*)$`).FindStringSubmatch(line)
		if fenced || m == nil {
			continue
		}
		m[1] = regexp.MustCompile(` +#+$`).ReplaceAllString(m[1], "")
		slug := strings.ReplaceAll(regexp.MustCompile(`[^\p{L}\p{N} _-]`).ReplaceAllString(strings.ToLower(m[1]), ""), " ", "-")
		if n := seen[slug]; n > 0 {
			seen[slug]++
			slug += "-" + strconv.Itoa(n)
		} else {
			seen[slug] = 1
		}
		texts = append(texts, m[1])
		anchors = append(anchors, slug)
	}
	return
}

// A heading inside a word's details counts for GitHub's duplicate numbering, so the link to a
// word must use the anchor of the word's own section, not the one of the heading in the details.
func TestAnchorsCountHeadingsInDetails(t *testing.T) {
	yaml := func(details string) string {
		return "namespace: ex.org/pay\npremise: P\nlanguage:\n  Charge:\n    desc: Take money.\n    see: [Invoice]\n" +
			"    details: |\n" + details + "  Invoice:\n    desc: A bill.\n"
	}
	for _, c := range []struct{ name, details, want string }{
		{"heading in details", "      ## Invoice\n\n      Notes.\n", "invoice-1"},
		{"closed ATX heading", "      ## Invoice ##\n", "invoice-1"},
		{"setext heading", "      Invoice\n      -------\n", "invoice-1"},
		{"same heading twice", "      ## Invoice\n\n      ## Invoice\n", "invoice-2"},
		{"heading in a code fence", "      ```\n      ## Invoice\n      ```\n", "invoice"},
		{"no heading", "      Invoice is a word here.\n", "invoice"},
	} {
		dir := t.TempDir()
		writeTree(t, dir, map[string]string{"UL.yaml": yaml(c.details)})
		te := newEnv(t, dir)
		te.mustOK("gen", "--root", dir)
		raw, err := os.ReadFile(filepath.Join(dir, "UL.md"))
		if err != nil {
			t.Fatal(err)
		}
		md := string(raw)
		texts, anchors := ghAnchors(md)
		own := -1 // the word's own section is the last Invoice heading (Invoice sorts after Charge)
		for i, h := range texts {
			if h == "Invoice" {
				own = i
			}
		}
		if own < 0 {
			t.Fatalf("%s: no Invoice section in\n%s", c.name, md)
		}
		if anchors[own] != c.want {
			t.Fatalf("%s: GitHub anchors the Invoice section %q, test expects %q\n%s", c.name, anchors[own], c.want, md)
		}
		if !strings.Contains(md, "| Charge | Take money. |  | [Invoice](#"+anchors[own]+") |") {
			t.Errorf("%s: the Invoice link must be #%s\n%s", c.name, anchors[own], md)
		}
	}
}

// --root resolves symlinks before ".." (the kernel does), so link/../docs is the real
// neighbour of what the link points to, not the neighbour of the link.
func TestRootDotDotAfterSymlink(t *testing.T) {
	base := t.TempDir()
	writeTree(t, base, map[string]string{
		"repo/sub/x.txt":    "x",
		"repo/docs/UL.yaml": ulYAML("ex.org/real", "Real"),
		"docs/UL.yaml":      ulYAML("ex.org/decoy", "Decoy"),
	})
	link := filepath.Join(base, "link")
	if err := os.Symlink(filepath.Join(base, "repo", "sub"), link); err != nil {
		t.Skip("no symlinks here")
	}
	for _, c := range []struct{ cwd, root string }{
		{base, link + "/../docs"},
		{base, "link/../docs"},
		{filepath.Join(base, "repo"), "../link/../docs"},
	} {
		r := newEnv(t, c.cwd).report("all", "--root", c.root)
		if ids := wordIDs(r); !slices.Equal(ids, []string{"ex.org/real#Real"}) {
			t.Errorf("cwd %s --root %s: %v, want only ex.org/real#Real", c.cwd, c.root, ids)
		}
	}
	// A root that does not exist keeps the old behaviour: refused with exit 2.
	if code, _, _ := newEnv(t, base).ul("all", "--root", "nope/../docs"); code != 0 && code != 2 {
		t.Errorf("missing root: exit %d", code)
	}
}

// A path git lists but ul cannot inspect stays in the scan, so load reports it as UL101; only
// a file that no longer exists drops out.
func TestScanKeepsUninspectablePath(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q")
	writeTree(t, dir, map[string]string{
		"ok/UL.yaml":  ulYAML("ex.org/ok", "Fine"),
		"sec/UL.yaml": ulYAML("ex.org/sec", "Secret"),
	})
	gitIn(t, dir, "add", "ok/UL.yaml", "sec/UL.yaml")
	if err := os.Chmod(filepath.Join(dir, "sec"), 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(filepath.Join(dir, "sec"), 0o755) })
	if _, err := os.Lstat(filepath.Join(dir, "sec", "UL.yaml")); err == nil {
		t.Skip("permissions are not enforced here (running as root)")
	}
	te := newEnv(t, dir)
	code, out, errs := te.ul("check")
	if code != 1 || !strings.Contains(out, "sec/UL.yaml:1: UL101 cannot read the file") {
		t.Fatalf("check: exit %d\nstdout: %s\nstderr: %s", code, out, errs)
	}
	if ids := wordIDs(te.report("all")); !slices.Equal(ids, []string{"ex.org/ok#Fine"}) {
		t.Fatalf("all must keep the readable UL: %v", ids)
	}
}

// indexStages writes a conflicted index entry: the same path at stages 1, 2 and 3.
func indexStages(t *testing.T, dir, rel string, bodies [3]string) {
	t.Helper()
	var lines strings.Builder
	for i, b := range bodies {
		hash := exec.Command("git", "hash-object", "-w", "--stdin")
		hash.Dir = dir
		hash.Stdin = strings.NewReader(b)
		out, err := hash.Output()
		if err != nil {
			t.Fatal(err)
		}
		lines.WriteString("100644 " + strings.TrimSpace(string(out)) + " " + strconv.Itoa(i+1) + "\t" + rel + "\n")
	}
	cmd := exec.Command("git", "update-index", "--index-info")
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(lines.String())
	cmd.Env = append(os.Environ(), "HOME="+t.TempDir(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("update-index: %v\n%s", err, out)
	}
}

// During a merge conflict git lists the conflicted UL.yaml once per index stage; ul scans it once.
func TestConflictedFileScannedOnce(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	good := ulYAML("ex.org/a", "Ay")
	for _, c := range []struct {
		name, worktree string
		wantCode       string
	}{
		{"resolved in the work tree, not yet staged", good, ""},
		{"conflict markers in the work tree", "<<<<<<< ours\n" + good + "=======\n" + good + ">>>>>>> theirs\n", "UL101"},
	} {
		dir := t.TempDir()
		gitIn(t, dir, "init", "-q")
		writeTree(t, dir, map[string]string{"a/UL.yaml": c.worktree})
		indexStages(t, dir, "a/UL.yaml", [3]string{good, good, good})
		if got := scanned(t, dir); !slices.Equal(got, []string{"a/UL.yaml"}) {
			t.Fatalf("%s: scan lists %v, want the file once", c.name, got)
		}
		r := newEnv(t, dir).report("all")
		var codes []string
		for _, p := range r.Problems {
			codes = append(codes, p.Code)
		}
		want := []string(nil)
		if c.wantCode != "" {
			want = []string{c.wantCode}
		}
		if !slices.Equal(codes, want) {
			t.Errorf("%s: problems %v, want %v (and never a false UL103)", c.name, codes, want)
		}
	}
}
