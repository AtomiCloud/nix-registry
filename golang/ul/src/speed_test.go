package main

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// genRepo writes `files` UL.yaml files of `per` words each. Every word has an alias, a
// details paragraph that names other words, bare and full-id references, so mentions and
// reference resolution do real work.
func genRepo(t *testing.T, dir string, files, per int) {
	t.Helper()
	m := map[string]string{}
	for f := 0; f < files; f++ {
		var b strings.Builder
		fmt.Fprintf(&b, "namespace: speed.example/area%03d/part\npremise: Generated area %d\nlanguage:\n", f, f)
		for w := 0; w < per; w++ {
			fmt.Fprintf(&b, "  Word%03d x%02d:\n    desc: Means thing %d of area %d and uses Word%03d x%02d.\n", f, w, w, f, f, (w+1)%per)
			fmt.Fprintf(&b, "    aliases: [Alias%03d x%02d]\n", f, w)
			fmt.Fprintf(&b, "    details: |\n      Long text for the word. It talks about Word%03d x%02d and Alias%03d x%02d,\n      and about Word%03d x%02d from another area; plus filler words that match nothing at all.\n",
				f, (w+3)%per, f, (w+2)%per, (f+1)%files, w)
			fmt.Fprintf(&b, "      %s\n", strings.Repeat("Some more filler prose that is long enough to scan. ", 6))
			fmt.Fprintf(&b, "    avoid: [nothing%d]\n    see: [Word%03d x%02d, speed.example/area%03d/part#Word%03d x%02d]\n", w, f, (w+1)%per, (f+1)%files, (f+1)%files, w)
		}
		m[fmt.Sprintf("area%03d/UL.yaml", f)] = b.String()
	}
	writeTree(t, dir, m)
}

// Spec §5: every command finishes in under one second on a repo with 1,000 words in 100
// files. Each command runs three times and the slowest run must stay under the limit; every
// measured time is logged.
func TestSpeed1000Words(t *testing.T) {
	const ceiling = time.Second
	for _, mode := range []string{"walk", "git"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			genRepo(t, dir, 100, 10)
			if mode == "git" {
				if _, err := exec.LookPath("git"); err != nil {
					t.Skip("no git")
				}
				gitIn(t, dir, "init", "-q")
			}
			te := newEnv(t, dir)
			te.vars["UL_SESSION"] = "speed"
			r := te.report("all", "--root", dir)
			if len(r.Words) != 1000 || len(r.Namespaces) != 100 || len(r.Problems) != 0 {
				t.Fatalf("generated repo: %d words, %d namespaces, problems %v", len(r.Words), len(r.Namespaces), r.Problems[:min(3, len(r.Problems))])
			}
			if len(findWord(t, r, "speed.example/area007/part#Word007 x03").Mentions) < 2 {
				t.Fatal("the generated words must mention each other")
			}
			te.mustOK("gen", "--root", dir) // UL.md exist, so gen --check and check have work to do
			cmds := map[string][]string{
				"all":         {"all", "--root", dir},
				"all-md":      {"all", "--root", dir, "--format", "md", "--details"},
				"lookup":      {"lookup", "alias050 x05", "--root", dir, "--from", "area042/x.go", "--format", "json"},
				"check":       {"check", "--root", dir},
				"gen --check": {"gen", "--check", "--root", dir},
			}
			for name, args := range cmds {
				worst := time.Duration(0)
				var runs []string
				for i := 0; i < 3; i++ {
					start := time.Now()
					code, out, errs := te.ul(args...)
					d := time.Since(start)
					if code != 0 {
						t.Fatalf("%s: exit %d\n%s%s", name, code, out[:min(200, len(out))], errs)
					}
					runs = append(runs, d.Round(time.Millisecond).String())
					worst = max(worst, d)
				}
				t.Logf("%-12s %s (%s mode, 1000 words in 100 files)", name, strings.Join(runs, " "), mode)
				if worst >= ceiling {
					t.Errorf("%s took %v at worst; the limit is %v", name, worst, ceiling)
				}
			}
		})
	}
}
