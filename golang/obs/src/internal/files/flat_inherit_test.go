package files

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Where platform comes from for a flat tier file follows obs.yaml's own lookup (obsyaml.Load):
// layout (i), observability/<feature>/, reads the nearest enclosing observability/obs.yaml;
// layout (ii), <feature>/observability/, never reads an obs.yaml above it, EXCEPT the obs.yaml at the Git root
// (a standalone observability repository), which every folder in the repository reads.
func TestFlatPlatformInheritedInLayoutOne(t *testing.T) {
	root := t.TempDir()
	write(t, root, ".git/HEAD", "ref")
	write(t, root, "observability/obs.yaml", "platform: tracker\n") // platform only here
	feature := filepath.Join(root, "observability", "friend")
	write(t, feature, "contract.yaml", "service: zinc\n")
	write(t, feature, "alerts/q/critical.yaml", retryQueueFlat)
	s, err := Load(feature)
	if err != nil || len(s.Problems) != 0 || len(s.Rules) != 1 {
		t.Fatalf("%v %+v", err, s.Problems)
	}
	if uid := s.Rules[0].Definition.UID; uid != "tracker-zinc-q-critical" {
		t.Fatalf("uid %q: platform tracker must come from the top observability/obs.yaml", uid)
	}
	// the feature's own obs.yaml wins over the top one
	write(t, feature, "obs.yaml", "platform: other\n")
	s, _ = Load(feature)
	if len(s.Rules) != 1 || s.Rules[0].Definition.UID != "other-zinc-q-critical" {
		t.Fatalf("own obs.yaml must win: %+v %+v", s.Rules, s.Problems)
	}
}

func TestFlatPlatformMissingInLayoutTwoNamesWhereToWriteIt(t *testing.T) {
	root := t.TempDir()
	write(t, root, ".git/HEAD", "ref")
	// decoys ON the path above the feature: layout (ii) never reads them (an ancestor not named observability,
	// and a root observability/ folder that is not an ancestor)
	write(t, root, "App/obs.yaml", "platform: tracker\n")
	write(t, root, "App/Friend/obs.yaml", "platform: tracker\n")
	write(t, root, "observability/obs.yaml", "platform: tracker\n")
	feature := filepath.Join(root, "App", "Friend", "observability")
	write(t, feature, "contract.yaml", "service: zinc\n")
	write(t, feature, "alerts/q/critical.yaml", retryQueueFlat)
	s, err := Load(feature)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Rules) != 0 || len(s.Problems) != 1 || s.Problems[0].Rule != "flat-alert-identity" ||
		!strings.Contains(s.Problems[0].Msg, "`platform:` in "+filepath.Join(feature, "obs.yaml")) || s.Problems[0].File != "alerts/q/critical.yaml" {
		t.Fatalf("layout (ii) never falls back: %+v %+v", s.Rules, s.Problems)
	}
	// writing it where the message says makes the rule build
	write(t, feature, "obs.yaml", "platform: tracker\n")
	s, _ = Load(feature)
	if len(s.Rules) != 1 || s.Rules[0].Definition.UID != "tracker-zinc-q-critical" {
		t.Fatalf("%+v %+v", s.Rules, s.Problems)
	}
}

// A repository with obs.yaml at its Git root is a standalone observability repository: every folder in it,
// layout (ii) included, uses that obs.yaml (the same one the rest of obs reads for the folder).
func TestFlatPlatformFromGitRootObsYamlInLayoutTwo(t *testing.T) {
	root := t.TempDir()
	write(t, root, ".git/HEAD", "ref")
	write(t, root, "obs.yaml", "platform: tracker\n")
	feature := filepath.Join(root, "App", "Friend", "observability")
	write(t, feature, "contract.yaml", "service: zinc\n")
	write(t, feature, "alerts/q/critical.yaml", retryQueueFlat)
	s, err := Load(feature)
	if err != nil || len(s.Problems) != 0 || len(s.Rules) != 1 {
		t.Fatalf("%v %+v", err, s.Problems)
	}
	if uid := s.Rules[0].Definition.UID; uid != "tracker-zinc-q-critical" {
		t.Fatalf("uid %q: platform tracker must come from the Git-root obs.yaml", uid)
	}
	if want := filepath.Join(root, "obs.yaml") + " platform"; !strings.HasSuffix(s.ident.PlatformFrom, "obs.yaml platform") ||
		!strings.Contains(s.ident.PlatformFrom, filepath.Base(root)) {
		t.Fatalf("platform read from %q, want %q", s.ident.PlatformFrom, want)
	}
	// without the Git-root file the same folder is refused, naming where to write platform
	if err := os.Remove(filepath.Join(root, "obs.yaml")); err != nil {
		t.Fatal(err)
	}
	s, _ = Load(feature)
	if len(s.Rules) != 0 || len(s.Problems) != 1 || s.Problems[0].Rule != "flat-alert-identity" {
		t.Fatalf("%+v %+v", s.Rules, s.Problems)
	}
}
