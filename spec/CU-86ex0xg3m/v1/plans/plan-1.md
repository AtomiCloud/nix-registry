# Plan 1: Add @krodak/clickup-cli to nix-registry

## Overview

This plan adds `@krodak/clickup-cli` (v1.8.0), a Node.js CLI tool that provides comprehensive ClickUp API access via the `cup` binary, to the nix-registry's Node 22 package set.

The nix-registry manages Node.js packages through `node2nix`, which generates Nix expressions from a `node-packages.json` manifest. CLI packages are then exposed in `export.nix` — often wrapped with a shell script that ensures Node.js is on `PATH` so the binary can run correctly. Every package must also be explicitly listed in the CI build job, which builds all packages and verifies their version output.

## Approach

**Step 1 — Register the package.** Add `"@krodak/clickup-cli"` to `node/22/node-packages.json`, the manifest that drives `node2nix`. Then run `direnv exec . pls gen:node:22` to regenerate the three generated files (`node-packages.nix`, `composition.nix`, `node-env.nix`). This adds the package source and its transitive dependencies to the Nix build graph.

**Step 2 — Expose the binary.** Add `clickup_cli` to `node/22/export.nix` so it's available as `.#clickup_cli` from the flake. Since `cup` needs Node.js at runtime, we wrap it using the `trivialBuilders.writeShellScriptBin` pattern — the same approach used by `sg` and `happy_coder` already in that file. The wrapper prepends `${nodejs}/bin` to `PATH` before invoking the binary.

**Step 3 — Update CI.** The CI build job in `.github/workflows/ci.yaml` explicitly lists every package in a `nix shell` call followed by version checks. Add `.#clickup_cli` to the shell list and `cup --version` to the verification block so CI validates the new package on every push.

**Step 4 — Verify locally.** Run `direnv exec . nix build .#clickup_cli` to confirm the package builds successfully and the `cup` binary is functional.

## Definition of Done

- [ ] `@krodak/clickup-cli` is available as a package in the nix-registry
- [ ] `direnv exec . nix build .#clickup_cli` succeeds
- [ ] The `cup` binary is accessible and functional via the wrapped export
- [ ] CI build job includes `clickup_cli` in the `nix shell` and version check
- [ ] All existing packages continue to build unaffected
- [ ] Pre-commit hooks pass (treefmt, gitlint, shellcheck)

## Implementation Checklist

### 1. Add package to node-packages.json

- [ ] Add `"@krodak/clickup-cli"` to `node/22/node-packages.json`

### 2. Regenerate node2nix files

- [ ] Run `direnv exec . pls gen:node:22` to regenerate:
  - `node/22/node-packages.nix`
  - `node/22/composition.nix`
  - `node/22/node-env.nix`
- [ ] Verify only `@krodak/clickup-cli` and its dependencies were added (no existing packages removed)

### 3. Expose clickup_cli in node/22/export.nix

- [ ] Add `clickup_cli_raw = n."@krodak/clickup-cli";` to the export set
- [ ] Add `clickup_cli` wrapper using `trivialBuilders.writeShellScriptBin` with Node.js on PATH, calling the `cup` binary
- [ ] Follow the same pattern as `sg` and `happy_coder` in the same file

### 4. Add to CI build job

- [ ] Add `.#clickup_cli` to the `nix shell` list in `.github/workflows/ci.yaml` build job
- [ ] Add `cup --version &&` to the version verification bash block

### 5. Build verification

- [ ] `direnv exec . nix build .#clickup_cli` succeeds
- [ ] Built binary provides `cup` command

### Out of Scope (handled externally)

- Committing — handled by kagent commit agent
- PR creation — handled by kagent polish phase
