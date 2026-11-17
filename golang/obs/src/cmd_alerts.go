package main

import (
	"flag"
	"fmt"
	"github.com/AtomiCloud/obs/internal/alerts"
	"io"
	"os"
)

func runAlerts(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("obs alerts", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var o alerts.Options
	fs.StringVar(&o.Dir, "dir", "observability", "observability folder containing GrafanaAlertRuleGroup CRs")
	fs.StringVar(&o.SeedPath, "seed", "", "seed path (default <dir>/seed.yaml)")
	fs.StringVar(&o.VLSettings, "vl-settings", "", "VictoriaLogs chart args YAML (default <dir>/victorialogs-settings.yaml, else pinned defaults)")
	fs.Int64Var(&o.Base, "base", 0, "unix seconds of the first run; the complete time line must end in the past")
	fs.IntVar(&o.Workers, "workers", 0, "evaluation workers (default all CPUs)")
	fs.StringVar(&o.Out, "out", "", "write JSON results")
	fs.StringVar(&o.Steps, "steps", "", "write per-minute JSON results; bypasses the result cache")
	fs.StringVar(&o.Recorded, "recorded", "", "write recording-rule samples as JSON; bypasses the result cache")
	fs.StringVar(&o.TempoConfig, "tempo-config", "", "Tempo config file whose query_frontend end cutoff and metrics limits trace rules run under (default Tempo 3.1.1: 30s, 24h, 10000)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 || o.Workers < 0 {
		fmt.Fprintln(stderr, "obs alerts: unexpected argument or negative --workers")
		return 2
	}
	if os.Getenv("OBS_VERIFY_PRUNE") == "1" {
		return runAlertsVerifyPrune(o, pruneForwardedFlags(fs), stdout, stderr) // cmd_alerts_verify.go
	}
	return alerts.Check(o, stdout, stderr)
}
