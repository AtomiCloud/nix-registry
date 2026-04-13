# Triage: Bundle PagerDuty CLI (pagerduty-cli) into nix-registry

## Delivery Kind

pr

## Complexity

straightforward

## Assessment

Add the `pagerduty-cli` npm package (provides `pd` command) to the nix-registry using the established node2nix packaging pattern. The package is a pure JavaScript oclif CLI with no native dependencies, identical in structure to the recently added `@krodak/clickup-cli`. The work follows a well-established, repeatable pattern: add to `node-packages.json`, regenerate with node2nix, export in `export.nix` with a shell wrapper (for PATH), and add CI verification. A `/node-package` skill exists that automates this exact workflow.

## Clarifications

None needed — the ticket is clear: package the npm `pagerduty-cli` as a nix derivation exposing the `pd` CLI command.

## Risks

Low risk — justified by:

- **No native dependencies**: pagerduty-cli is pure JavaScript (axios, simple-oauth2, oclif). No node-gyp, no platform-specific binaries.
- **Established pattern**: Identical to the clickup-cli addition (commit `4de7b2d`), which used the same oclif framework and shell wrapper approach.
- **Isolated changes**: Only touches `node/22/node-packages.json`, `node/22/export.nix`, generated `node/22/node-packages.nix`, and `.github/workflows/ci.yaml`. No shared configs or interfaces affected.
- **CI catches breakage**: The CI build step verifies all packages build on both amd64 and arm64, and runs version/help commands.

The only minor risk is that node2nix dependency resolution may surface transitive dependency conflicts, but this is caught immediately at generation time.

## Verification

### Assumptions to Verify

- The npm package `pagerduty-cli` (latest 0.1.18) resolves cleanly through node2nix without dependency conflicts. **Source**: Running `task gen:node:22` (or `pls gen:node:22`) will confirm.
- The `pd` binary works when invoked via the node2nix derivation (may need a PATH wrapper like clickup-cli/happy-coder since oclif CLIs sometimes need Node on PATH). **Source**: `nix build .#pagerduty_cli && ./result/bin/pd --version`.

### Access Required

None — all work is local nix builds and npm registry access.

### Testing Level

light
The package follows an identical pattern to 8 existing node packages. CI already validates builds on both architectures. A local `nix build` + `pd --version` is sufficient.

### Validation Matrix

- Automated immediate: CI builds `.#pagerduty_cli` on amd64 and arm64; runs `pd --version` in the verification step
- Manual immediate: none
- Automated post-release: none
- Manual post-release: none
