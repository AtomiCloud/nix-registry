// Package gen is the code-generation driver of `obs gen` (concept section 3.4): every language
// generator registers here, and the driver runs them, writes the files, and checks that the
// committed files are neither hand-edited nor stale (`obs gen --check`).
//
// A generator writes types and constants only. It is a pure function of the contract:
// the same contract gives the same bytes, formatted the way the language's formatter would.
package gen

import (
	"fmt"
	"sort"
	"sync"

	"github.com/AtomiCloud/obs/internal/contract"
)

// File is one generated file. Path is relative to the output folder the target names.
type File struct {
	Path    string
	Content []byte
}

// Options carry the per-target settings of the contract's `generate:` entry.
type Options struct {
	Package    string // go package name, dart library name
	Namespace  string // .NET namespace
	Interfaces string // dependency import/module/namespace (defaults to the published package)
	PackageID  string // owning package identity resolved from the target's manifest
	ContractID string // complete manifest-relative registration identity
}

// Generator writes the typed signals of one language.
type Generator interface {
	// Lang is the language key used in contract `generate:` (go, dotnet, dart, bun).
	Lang() string
	// Files returns every file of the language for a VALID contract, in any order
	// (the driver sorts). Contents must start with the header from Header().
	Files(c *contract.Contract, o Options) ([]File, error)
}

var (
	mu       sync.Mutex
	registry = map[string]Generator{}
)

// Register adds a generator. Each generator package calls it from init(); the one place that
// imports them all is registry.go.
func Register(g Generator) {
	mu.Lock()
	defer mu.Unlock()
	if _, dup := registry[g.Lang()]; dup {
		panic("gen: duplicate generator for " + g.Lang())
	}
	registry[g.Lang()] = g
}

// Get returns the generator of a language.
func Get(lang string) (Generator, bool) {
	mu.Lock()
	defer mu.Unlock()
	g, ok := registry[lang]
	return g, ok
}

// Langs lists the registered languages, sorted.
func Langs() []string {
	mu.Lock()
	defer mu.Unlock()
	out := make([]string, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Generate runs one generator and returns its files sorted by path.
func Generate(lang string, c *contract.Contract, o Options) ([]File, error) {
	g, ok := Get(lang)
	if !ok {
		return nil, fmt.Errorf("no generator for language %q (have %v)", lang, Langs())
	}
	fs, err := g.Files(c, o)
	if err != nil {
		return nil, fmt.Errorf("%s generator: %w", lang, err)
	}
	sort.Slice(fs, func(i, j int) bool { return fs[i].Path < fs[j].Path })
	return fs, nil
}
