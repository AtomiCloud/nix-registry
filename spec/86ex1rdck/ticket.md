# Bundle PagerDuty CLI (pagerduty-cli) into nix-registry

- **ID**: 86ex1rdck
- **Status**: in progress
- **Priority**: none
- **URL**: https://app.clickup.com/t/86ex1rdck

## Description

The pagerduty-cli npm package (provides pd command) is not available in nixpkgs. Package it as a nix derivation in our nix-registry so it can be used in flakes across projects.

Context:

npm package: pagerduty-cli by martindstone
Repo: https://github.com/martindstone/pagerduty-cli
Provides pd CLI for PagerDuty incident management, on-call queries, service listing
Needed for AI agent automation of Liftoff PE work (observability/incident response)
Other CLIs (logcli, promtool, grafanactl) are already in nixpkgs
