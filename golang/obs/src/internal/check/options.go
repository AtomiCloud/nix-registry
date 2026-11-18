package check

import (
	"io"
	"os"
	"path/filepath"

	"github.com/AtomiCloud/obs/internal/contract"
	"github.com/AtomiCloud/obs/internal/obsyaml"
)

// Options of `obs query`.
type Options struct {
	Dir        string // the observability folder
	SeedPath   string // default <Dir>/seed.yaml
	VLSettings string // default <Dir>/victorialogs-settings.yaml, else pinned VL defaults
	Base       int64  // unix seconds of the seed's start (0 = aligned, ending in the past)
	Names      bool   // print the names each query reads
	// Obs is <Dir>/obs.yaml (nil when absent): its thresholds sit under --vl-settings.
	Obs *obsyaml.File
	// EventRule says when a LogsQL alert must name a contract event:
	// "auto" (only when <Dir>/contract.yaml exists; the decided default), "on", "off".
	EventRule string
	// Contract is the parsed <Dir>/contract.yaml; Static loads it when nil and the file exists.
	Contract       *contract.Contract
	SpanAttributes SpanAttributeNames
	Out            io.Writer
}

func (o *Options) contractPath() string { return filepath.Join(o.Dir, "contract.yaml") }

func (o *Options) hasContract() bool {
	_, err := os.Stat(o.contractPath())
	return err == nil
}

func (o *Options) eventRuleOn() bool {
	switch o.EventRule {
	case "on":
		return true
	case "off":
		return false
	}
	return o.hasContract()
}
