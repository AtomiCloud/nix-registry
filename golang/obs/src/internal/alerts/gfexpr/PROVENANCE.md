# Grafana expressions

Source read: grafana v13.3.0 (commit 752e3462).

`mathexp/` and `mathexp/parse/` are the Grafana `pkg/expr/mathexp/` source,
carried through the obs spike. AGPL-3.0-only; the licence is
`LICENSE-grafana-AGPL`. The Go parser files retain their Go Authors notices.
Upstream source: https://github.com/grafana/grafana/tree/752e3462/pkg/expr/mathexp

Changes: imports point to this module; backend tracing and logging are removed
from the expression evaluator; `light.go` is the obs numeric-only evaluation path
that mirrors the upstream node walk and label joins. `OBS_VERIFY_FAST=1`
compares it with the dataframe evaluator on every eligible tick.

The adjacent `rules.go` ports Grafana `pkg/expr/{reduce,threshold}.go`,
`pkg/services/ngalert/eval/eval.go`, and the state machine in
`pkg/services/ngalert/state/{state,manager,cache}.go`. Each transition names its
upstream function. Notification delivery, persistence and scheduling are outside
this offline evaluator.
