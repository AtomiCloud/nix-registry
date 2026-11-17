package check

import (
	"io"
	"os"
	"path/filepath"
)

// Options of `obs query`.
type Options struct {
	Dir        string // the observability folder
	SeedPath   string // default <Dir>/seed.yaml
	VLSettings string // default <Dir>/victorialogs-settings.yaml, else pinned VL defaults
	Base       int64  // unix seconds of the seed's start (0 = aligned, ending in the past)
	Names      bool   // print the names each query reads
	// EventRule says when a LogsQL alert must name a contract event:
	// "auto" (only when <Dir>/contract.yaml exists; the decided default), "on", "off".
	EventRule string
	Out       io.Writer
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
