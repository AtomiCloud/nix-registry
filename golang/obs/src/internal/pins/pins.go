// Package pins owns the versions used by obs and checked before nightly parity.
package pins

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// Pin identifies an upstream release and, when required, its source commit.
type Pin struct {
	Version string `json:"version"`
	Commit  string `json:"commit,omitempty"`
	// Digest is the container image digest (sha256:...) the parity job and a digest-pinned chart use.
	Digest string `json:"digest,omitempty"`
	// Also lists further image versions of the same component whose digest is known
	// (an older or newer release a sandbox may run). Only the sandbox reads it; the
	// engines built into obs, the parity job and the chart check use Version.
	Also []Image `json:"also,omitempty"`
}

// Image is one known container image: the tag exactly as the registry spells it and
// the digest recorded for it.
type Image struct {
	Version string `json:"version"`
	Digest  string `json:"digest"`
}

// digestRe is a container image digest as registries print it.
var digestRe = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// ValidDigest tells whether d is sha256:<64 lower-case hex>. Nothing else (an empty
// string, another hash, upper case, padding) names an image.
func ValidDigest(d string) bool { return digestRe.MatchString(d) }

// Listed lists every image version pins.json records for the component, the pin first,
// then Also, whether or not its digest is usable. Use Images for the images obs may run.
func (p Pin) Listed() []Image {
	var out []Image
	if p.Version != "" {
		out = append(out, Image{Version: p.Version, Digest: p.Digest})
	}
	return append(out, p.Also...)
}

// Images lists every image version of a pinned component whose digest is known: the pin
// itself first, then Also, each only with a valid sha256 digest. Empty when the component
// has no pin or no valid digest.
func (p Pin) Images() []Image {
	var out []Image
	for _, im := range p.Listed() {
		if ValidDigest(im.Digest) {
			out = append(out, im)
		}
	}
	return out
}

// SameVersion tells whether two version spellings name one release ("v1.156.0" and
// "1.156.0" do).
func SameVersion(a, b string) bool {
	return strings.TrimPrefix(a, "v") == strings.TrimPrefix(b, "v")
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
