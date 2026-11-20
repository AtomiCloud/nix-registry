package obsyaml

import "testing"

// obs live owns the targets: and estimates: sections; obs.yaml must still load for every
// other command when they are present, and must hand them over with their line numbers.
func TestOwnedSectionsAreKeptNotRefused(t *testing.T) {
	src := "versions:\n  grafana: 13.3.0\ntargets:\n  prod:\n    metrics: https://vm.example\nestimates:\n  logs:\n    lines_per_min: 100\n"
	f, err := Parse("obs.yaml", []byte(src))
	if err != nil {
		t.Fatalf("a file with targets: and estimates: must load: %v", err)
	}
	if f.Versions.Grafana != "13.3.0" {
		t.Fatalf("versions lost: %+v", f.Versions)
	}
	n := f.Section("targets")
	if n == nil || n.Line != 4 {
		t.Fatalf("targets section node missing or wrong line: %+v", n)
	}
	if f.Section("estimates") == nil {
		t.Fatal("estimates section missing")
	}
	if f.Section("nothing") != nil {
		t.Fatal("an absent section must be nil")
	}
}

func TestOtherUnknownKeysStillRefused(t *testing.T) {
	if _, err := Parse("obs.yaml", []byte("targets:\n  a: {}\nbogus: 1\n")); err == nil {
		t.Fatal("an unknown top-level key must still fail")
	}
}
