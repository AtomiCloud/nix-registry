# Plan 1: Add pagerduty-cli, remove happy-coder, upgrade all Node 22 packages

## Overview

Add the `pagerduty-cli` npm package to the nix-registry, exposing the `pd` CLI command. Remove the unused `happy-coder` package. Upgrade all existing Node 22 packages to their latest npm versions by leaving entries unpinned during node2nix regeneration. All changes form a single committable unit.

**Spec deviation**: The spec says "Out of Scope: Upgrading other existing packages" and does not mention removing `happy-coder`. The user has explicitly approved both changes as acceptable out-of-spec additions.

## Changes

### 1. `node/22/node-packages.json` — Update manifest

Replace the file contents with all entries unpinned, `happy-coder` removed, `pagerduty-cli` added:

```json
[
  "@upstash/cli",
  "action-docs",
  "typescript-json-schema",
  "swagger-typescript-api",
  "@kirinnee/semantic-generator",
  "openapi-to-postmanv2",
  "@krodak/clickup-cli",
  "pagerduty-cli"
]
```

8 entries total (was 9 with happy-coder, now 8 with happy-coder removed and pagerduty-cli added).

### 2. `node/22/node-packages.nix` and `node/22/composition.nix` — Regenerate

Run `task gen:node:22` to regenerate with all packages resolved to their latest npm versions. These are generated files — no manual edits.

### 3. `node/22/export.nix` — Remove happy-coder, add pagerduty-cli, update attribute references

Three sub-changes:

**a) Remove happy-coder entries** — Delete `happy_coder_raw` and the `happy_coder` shell wrapper (currently lines 26-33).

**b) Add pagerduty-cli wrapper** — Following the `clickup_cli` pattern:

- `pagerduty_cli_raw = n."pagerduty-cli";` — raw node2nix derivation
- `pagerduty_cli` — shell wrapper via `trivialBuilders.writeShellScriptBin` with:
  - `name = "pd"`
  - `version = pagerduty_cli_raw.version`
  - PATH export including `${nodejs}/bin`
  - Exec: `${pagerduty_cli_raw}/bin/pd "$@"`

**c) Update version-suffixed attribute names** — After regeneration, node2nix may produce different version-suffixed attribute names in `composition.nix` (e.g., `swagger-typescript-api-13.6.5` may become `swagger-typescript-api-14.x.x`). The implementer MUST check `composition.nix` for the actual attribute names of all top-level packages and update `export.nix` references to match. Specifically check:

- `n."swagger-typescript-api-..."` (currently `13.6.5`)
- `n."@krodak/clickup-cli-..."` (currently `1.8.0`)
- `n."pagerduty-cli"` or `n."pagerduty-cli-x.y.z"` (new, check which form node2nix produces)

### 4. `.github/workflows/ci.yaml` — Require pagerduty-cli CI verification

The CI file MUST contain:

- `.#pagerduty_cli` in the `nix shell` package list
- `pd --version &&` in the version check chain

These were added during kloop iterations and should already be present. If missing, add `.#pagerduty_cli` after `.#clickup_cli` in the shell list, and `pd --version &&` after `cup --version &&` in the check chain. No happy-coder entries exist in CI, so no removal needed there.

## Spec Adherence

| Spec Requirement                             | Covered                   |
| -------------------------------------------- | ------------------------- |
| FR1: Add to node-packages.json               | Change 1                  |
| FR2: Regenerate node2nix files               | Change 2                  |
| FR3: Export with shell wrapper               | Change 3                  |
| FR4: CI verification                         | Change 4                  |
| FR5: `pd --version` succeeds                 | Validated via build + run |
| NFR1: Linting (`nix fmt`)                    | Validation step           |
| NFR2: Building (`nix build .#pagerduty_cli`) | Validation step           |
| NFR3: Invariant (PATH + "$@" forwarding)     | Shell wrapper pattern     |
| NFR4: Cross-architecture                     | CI matrix (amd64 + arm64) |

Additional out-of-spec changes (user-approved):

- Remove `happy-coder` package (Change 1, Change 3)
- Upgrade all existing packages to latest (Change 1, Change 2, Change 3c)

## Acceptance Criteria

### Functional Checks

- `nix build .#pagerduty_cli` succeeds (derivation builds without errors)
- `./result/bin/pd --version` prints a version string and exits 0
- The shell wrapper correctly sets `PATH` to include Node.js and forwards all arguments via `"$@"`
- `happy-coder` is absent from `node-packages.json` and `export.nix`
- `.github/workflows/ci.yaml` contains `.#pagerduty_cli` in the nix shell list and `pd --version &&` in the version check chain
- All other existing packages still build: `nix build .#sg`, `.#upstash`, `.#action_docs`, `.#typescript_json_schema`, `.#swagger_typescript_api`, `.#openapi_to_postmanv2`, `.#clickup_cli`

### Non-Functional Checks

- `nix fmt` produces no changes on committed files (lint passes)
- Generated `node-packages.nix` resolves all transitive dependencies without conflict
- CI build job passes on both amd64 and arm64 (verified post-push via CI matrix)
- Attribute names in `export.nix` match the regenerated `composition.nix` (no stale version-suffixed references)

## Validation Approach

**Immediate automated checks (dev loop):**

1. After `task gen:node:22`: verify `node/22/node-packages.nix` contains `pagerduty-cli` entries and does NOT contain `happy-coder` entries
2. Verify `export.nix` attribute names match `composition.nix` (grep for each top-level package in composition.nix and confirm export.nix references the correct version-suffixed name)
3. `nix build .#pagerduty_cli` — must succeed
4. `./result/bin/pd --version` — must print version and exit 0
5. `nix fmt -- --check` on modified `.nix` files — must pass
6. Build-test at least one upgraded package (e.g., `nix build .#clickup_cli`) to confirm upgrades didn't break anything

**Post-release checks:**

- CI pipeline validates build on both amd64 and arm64 via the existing matrix
- CI runs `pd --version` in the version check chain
- CI runs all other package version checks, confirming upgrades didn't break them

**Manual checks:**

- None required — fully automated validation
