# Spec: Add pagerduty-cli (pd) to nix-registry

## Summary

Add the `pagerduty-cli` npm package to the nix-registry as a Node 22 package, following the established node2nix pattern. This exposes the `pd` CLI command for PagerDuty incident management, needed for AI agent automation of Liftoff PE work.

## Verification Evidence

- **Assumption: npm package `pagerduty-cli` resolves cleanly through node2nix**
  - Checked: npm registry listing at https://www.npmjs.com/package/pagerduty-cli and GitHub repo package.json at https://github.com/martindstone/pagerduty-cli
  - Confirmed: Package exists on npm. It is a pure JavaScript oclif CLI with dependencies on `@oclif/core`, `axios`, `simple-oauth2`, `chrono-node`, `csv-parse` — no native/node-gyp dependencies. Requires Node >=16 (compatible with Node 22). Version on GitHub is 0.1.17. Will be confirmed definitively when `task gen:node:22` runs successfully during implementation.

- **Assumption: The `pd` binary works via node2nix derivation (may need PATH wrapper)**
  - Checked: GitHub package.json `bin` field: `"pd": "bin/run"` — standard oclif entry point pattern.
  - Confirmed: Binary name is `pd`. Since it's an oclif CLI (same framework as `@krodak/clickup-cli`), it will need the same PATH wrapper pattern to ensure Node.js is available at runtime. This matches the `clickup_cli` wrapper at `node/22/export.nix:35-43`.

## Requirements

### Functional Requirements

1. **Add `pagerduty-cli` to the node2nix manifest**: Add `"pagerduty-cli"` to the JSON array in `node/22/node-packages.json` (currently 8 entries, becomes 9).

2. **Regenerate node2nix files**: Run `task gen:node:22` (or `pls gen:node:22`) to regenerate `node/22/node-packages.nix` and `node/22/composition.nix` with the new package and all transitive dependencies resolved.

3. **Export with shell wrapper in `node/22/export.nix`**: Add a wrapped derivation following the clickup-cli pattern:
   - `pagerduty_cli_raw = n."pagerduty-cli";` — raw node2nix derivation
   - `pagerduty_cli` — shell wrapper via `trivialBuilders.writeShellScriptBin` with:
     - `name = "pd"` (the CLI binary name)
     - `version = pagerduty_cli_raw.version`
     - PATH export including `${nodejs}/bin`
     - Calls `${pagerduty_cli_raw}/bin/pd "$@"`

4. **Add CI verification in `.github/workflows/ci.yaml`**:
   - Add `.#pagerduty_cli` to the `nix shell` package list (after `.#clickup_cli` at line 68)
   - Add `pd --version &&` to the version check chain (after `cup --version &&` at line 91)
   - The CI must invoke `--version` (or the appropriate version command) to verify the binary actually runs, not just that it exists

5. **`pd --version` must succeed**: After `nix build .#pagerduty_cli`, running `./result/bin/pd --version` must print a version string and exit 0.

### Non-Functional Requirements

1. **Linting** — The new/modified Nix files must pass `nix fmt` (nixpkgs-fmt). The CI YAML must pass the existing actionlint check. No new lint rules needed.

2. **Building** — `nix build .#pagerduty_cli` must succeed. The node2nix generation must resolve all transitive dependencies without conflict. Build must succeed on both amd64 and arm64 (verified by CI matrix).

3. **Invariant Checking** — The shell wrapper must correctly set PATH and forward all arguments (`"$@"`). The CI version check enforces this invariant on every build.

4. **Cross-architecture support** — The derivation must build on both `x86_64-linux` and `aarch64-linux` (enforced by CI matrix with nscloud runners).

## Acceptance Criteria

1. `nix build .#pagerduty_cli` succeeds locally
2. `./result/bin/pd --version` prints a version string and exits 0
3. `nix fmt` passes with no changes to committed files
4. CI build job passes on both amd64 and arm64 (verified after push)
5. Commit uses `new(pagerduty-cli): Add pagerduty-cli for PagerDuty incident management` format

## Out of Scope

- Configuring PagerDuty API tokens or OAuth setup
- Adding `pagerduty_cli` to `nix/registry.nix` (can be done later if needed in dev shells)
- Testing actual PagerDuty API functionality (requires auth tokens)
- Upgrading other existing packages
