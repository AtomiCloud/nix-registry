package sandbox

import (
	"encoding/json"
	"io"
	"os"
	"testing"
)

// GPT re-review of PR 133: writeState replaces the file in one step. A reader that holds the earlier file
// open still sees the complete earlier state after the new one appears (a truncating write would not).
func TestWriteStateReplacesTheFileInOneStep(t *testing.T) {
	cfg := Config{Project: "demo", StateDir: t.TempDir()}
	earlier := &Info{Project: "demo", Grafana: "http://g", From: 111, To: 222, TracesSeeded: true}
	if err := writeState(cfg, earlier); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(stateFile(cfg))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := writeState(cfg, &Info{Project: "demo", Grafana: "http://g", SeedIncomplete: true}); err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	var old Info
	if err := json.Unmarshal(b, &old); err != nil || old.From != 111 || old.To != 222 || !old.TracesSeeded {
		t.Fatalf("the open earlier file lost its complete state: %q (%v)", b, err)
	}
	if now := readState(cfg); now == nil || !now.SeedIncomplete || now.From != 0 {
		t.Fatalf("the new state is not in place: %+v", now)
	}
}

// The fingerprint of the rules a forecast was computed for lives in the state file only.
func TestRulesFingerprintStaysOutOfThePublicJSON(t *testing.T) {
	cfg := Config{Project: "demo", StateDir: t.TempDir()}
	i := &Info{Project: "demo", Grafana: "http://g"}
	i.rulesKey = "abc"
	if b, _ := json.Marshal(i); string(b) != string(mustJSON(t, &Info{Project: "demo", Grafana: "http://g"})) {
		t.Fatalf("the fingerprint is in the public JSON: %s", b)
	}
	if err := writeState(cfg, i); err != nil {
		t.Fatal(err)
	}
	if got := readState(cfg); got == nil || got.rulesKey != "abc" {
		t.Fatalf("the state file lost the fingerprint: %+v", got)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
