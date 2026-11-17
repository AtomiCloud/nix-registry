package all_test

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AtomiCloud/obs/internal/contract"
	"github.com/AtomiCloud/obs/internal/gen"
	_ "github.com/AtomiCloud/obs/internal/gen/all"
	"github.com/AtomiCloud/obs/internal/testfix"
)

// Generator fuzz (fast half): every contract in tests/fixtures/fuzz is valid, and for each language
// the generator must finish and either refuse with an error that names the problem, or return files
// that are the same on a second run. That the files COMPILE is tests/fuzz.sh (it needs the compilers).
func TestFuzzContractsRefuseOrGenerate(t *testing.T) {
	files, _ := filepath.Glob(testfix.Path(t, "fixtures", "fuzz", "*.yaml"))
	if len(files) == 0 {
		t.Fatal("no fuzz contracts")
	}
	for _, f := range files {
		c, err := contract.Load(f)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if is := c.Validate(); len(is) > 0 {
			t.Fatalf("%s: a fuzz case must be a valid contract: %v", f, is)
		}
		for _, lang := range gen.Langs() {
			name := filepath.Base(f) + "/" + lang
			type result struct {
				fs  []gen.File
				err error
			}
			done := make(chan result, 1)
			go func() {
				fs, err := gen.Generate(lang, c, gen.Options{Package: "fuzz", Namespace: "Fuzz.Signals"})
				done <- result{fs, err}
			}()
			select {
			case r := <-done:
				if r.err != nil {
					if strings.TrimSpace(r.err.Error()) == "" {
						t.Errorf("%s: refused without a message", name)
					}
					continue
				}
				if len(r.fs) == 0 {
					t.Errorf("%s: no files and no error", name)
				}
				again, _ := gen.Generate(lang, c, gen.Options{Package: "fuzz", Namespace: "Fuzz.Signals"})
				for i := range r.fs {
					if i >= len(again) || !bytes.Equal(r.fs[i].Content, again[i].Content) {
						t.Errorf("%s: two runs differ in %s", name, r.fs[i].Path)
					}
				}
			case <-time.After(20 * time.Second):
				t.Errorf("%s: the generator did not finish", name)
			}
		}
	}
}
