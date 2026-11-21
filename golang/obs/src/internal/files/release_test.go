package files

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestReleaseFlatIdentitySkip(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "obs.yaml", flatObs)
	write(t, dir, "alerts/q/warning.yaml", retryQueueFlat)
	clean, err := LoadScoped(dir, nil, dir)
	if err != nil {
		t.Fatal(err)
	}
	write(t, dir, "contract.yaml", flatContract)
	skipped, err := LoadScoped(dir, func(p string) bool { return p == filepath.Join(dir, "contract.yaml") }, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(clean.Problems, skipped.Problems) || len(skipped.Rules) != 0 {
		t.Fatalf("skipped contract supplied identity: clean=%v got=%v rules=%d", clean.Problems, skipped.Problems, len(skipped.Rules))
	}
}

func TestReleaseFlatUID(t *testing.T) {
	for _, slug := range []string{"charge.err", strings.Repeat("x", 41)} {
		s := flatDir(t, "alerts/"+slug+"/warning.yaml", retryQueueFlat)
		uid := "tracker-zinc-" + slug + "-warning"
		if len(s.Rules) != 0 || len(s.Problems) != 1 || !strings.HasPrefix(s.Problems[0].Rule, "flat-alert-uid") || !strings.Contains(s.Problems[0].Msg, uid) || !strings.Contains(s.Problems[0].Msg, slug) {
			t.Errorf("invalid uid accepted or not named: %s: %v", uid, s.Problems)
		}
	}
}

func TestReleaseUnreadablePayloadEntry(t *testing.T) {
	for _, target := range []string{"missing", "alerts"} {
		dir := t.TempDir()
		if err := os.Symlink(target, filepath.Join(dir, "alerts")); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "alerts") {
			t.Errorf("unreadable eligible payload is absent: %v", err)
		}
		if _, err := LoadSkip(dir, func(p string) bool { return p == filepath.Join(dir, "alerts") }); err != nil {
			t.Errorf("pruned payload read: %v", err)
		}
	}
}

func TestReleasePayloadPermissionError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read mode-000 entries")
	}
	dir := t.TempDir()
	write(t, dir, "alerts/q/warning.yaml", retryQueueFlat)
	file := filepath.Join(dir, "alerts/q/warning.yaml")
	if err := os.Chmod(file, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(file, 0600) })
	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), file) {
		t.Fatalf("permission failure treated as absent: %v", err)
	}
}
