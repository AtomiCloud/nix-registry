package check

import (
	"io"
	"os"
	"path/filepath"

	"github.com/AtomiCloud/obs/internal/contract"
	"github.com/AtomiCloud/obs/internal/engines/tempo"
	"github.com/AtomiCloud/obs/internal/obsyaml"
)

// Options of `obs query`.
type Options struct {
	Dir        string // the observability folder
	SeedPath   string // default <Dir>/seed.yaml
	VLSettings string // default <Dir>/victorialogs-settings.yaml, else pinned VL defaults
	Base       int64  // unix seconds of the seed's start (0 = aligned, ending in the past)
	Names      bool   // print the names each query reads
	// TempoConfig is a Tempo config file (--tempo-config) whose query_frontend settings
	// override obs.yaml thresholds.traces for trace panels.
	TempoConfig string
	// TempoFrontend is the resolved query-frontend setting trace panels run under: Tempo's
	// defaults, then obs.yaml thresholds.traces, then TempoConfig. Live fills it; nil means
	// Tempo's defaults.
	TempoFrontend *tempo.Frontend
	// Obs is <Dir>/obs.yaml (nil when absent): its thresholds sit under --vl-settings.
	Obs *obsyaml.File
	// EventRule says when a LogsQL alert must name a contract event:
	// "auto" (only when <Dir>/contract.yaml exists; the decided default), "on", "off".
	EventRule string
	// Contract is the parsed <Dir>/contract.yaml; Static loads it when nil and the file exists.
	Contract *contract.Contract
	// NoOtherContracts turns off reading the repository's other observability/contract.yaml files
	// (--contracts none). By default every one is read, so a dashboard may use a metric another contract declares.
	NoOtherContracts bool
	// metricsOnly is set by loadContract when Contract is the merged view of the repository's contracts for a folder
	// with none of its own: metric names are checked against it, logs, spans and events are not.
	metricsOnly bool
	// repoFiles are the contract files that view was merged from (relative to the Git root), for messages.
	repoFiles []string
	// AllowEmpty accepts a folder with no alert and no dashboard (the "empty" rule is off): `obs check` runs the query rules on whatever the folder has.
	AllowEmpty     bool
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
