package main

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testEnv runs ul in-process with its own environment, so tests never read the real
// $FY_SESSION_ID or the real state directory.
type testEnv struct {
	t    *testing.T
	cwd  string
	vars map[string]string
}

func newEnv(t *testing.T, cwd string) *testEnv {
	t.Helper()
	return &testEnv{t: t, cwd: cwd, vars: map[string]string{"UL_STATE_DIR": t.TempDir(), "HOME": t.TempDir()}}
}

func (te *testEnv) run(stdin string, args ...string) (code int, stdout, stderr string) {
	te.t.Helper()
	var out, errb bytes.Buffer
	code = run(args, &env{cwd: te.cwd, getenv: func(k string) string { return te.vars[k] },
		stdin: strings.NewReader(stdin), stdout: &out, stderr: &errb})
	return code, out.String(), errb.String()
}

func (te *testEnv) ul(args ...string) (int, string, string) { return te.run("", args...) }

// mustOK runs a command that must exit 0 and returns stdout.
func (te *testEnv) mustOK(args ...string) string {
	te.t.Helper()
	code, out, errs := te.ul(args...)
	if code != 0 {
		te.t.Fatalf("ul %v: exit %d\nstdout: %s\nstderr: %s", args, code, out, errs)
	}
	return out
}

func (te *testEnv) report(args ...string) *Report {
	te.t.Helper()
	var r Report
	if err := json.Unmarshal([]byte(te.mustOK(args...)), &r); err != nil {
		te.t.Fatalf("bad JSON from ul %v: %v", args, err)
	}
	return &r
}

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// fixture copies testdata/<name> into a fresh temp dir (outside any git repo, so the scan
// walks it).
func fixture(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	copyTree(t, filepath.Join("testdata", name), dir)
	return dir
}

// writeTree writes files (path -> content) under dir.
func writeTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func wordIDs(r *Report) []string {
	var ids []string
	for _, w := range r.Words {
		ids = append(ids, w.ID)
	}
	return ids
}

func findWord(t *testing.T, r *Report, id string) JWord {
	t.Helper()
	for _, w := range r.Words {
		if w.ID == id {
			return w
		}
	}
	t.Fatalf("word %s not in output (have %v)", id, wordIDs(r))
	return JWord{}
}

func jsonUnmarshal(s string, v any) error { return json.Unmarshal([]byte(s), v) }
