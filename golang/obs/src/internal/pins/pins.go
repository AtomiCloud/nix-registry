// Package pins owns the versions used by obs and checked before nightly parity.
package pins

import (
	_ "embed"
	"encoding/json"
)

// Pin identifies an upstream release and, when required, its source commit.
type Pin struct {
	Version string `json:"version"`
	Commit  string `json:"commit,omitempty"`
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
