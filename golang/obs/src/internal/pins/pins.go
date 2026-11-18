// Package pins owns the versions used by obs and checked before nightly parity.
package pins

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
)

// Pin identifies an upstream release and, when required, its source commit.
type Pin struct {
	Version string `json:"version"`
	Commit  string `json:"commit,omitempty"`
	// Digest is the container image digest (sha256:...) the parity job and a digest-pinned chart use.
	Digest string `json:"digest,omitempty"`
}

//go:embed pins.json
var source []byte

// All returns a fresh copy so callers cannot mutate the process's pins.
func All() map[string]Pin {
	var p map[string]Pin
	if err := json.Unmarshal(source, &p); err != nil {
		panic(err)
	}
	return p
}

// Load reads a pins file in the pins.json shape (a test seam for `obs engines --pins`).
func Load(path string) (map[string]Pin, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var p map[string]Pin
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, fmt.Errorf("%s: %v", path, err)
	}
	return p, nil
}
