# Task Spec: Add @krodak/clickup-cli to nix-registry

## Objective

Add `@krodak/clickup-cli` as a new npm-based package in the nix-registry's Node 22 package set, exposing the `cup` CLI binary and including it in CI.

## Package: `@krodak/clickup-cli`

- **npm package**: `@krodak/clickup-cli`
- **Version**: 1.8.0
- **License**: MIT
- **Source**: https://github.com/krodak/clickup-cli
- **Binary name**: `cup`
- **Requires**: Node.js 22+
- **Description**: A comprehensive ClickUp CLI tool providing full ClickUp API coverage. Designed for AI agent usage with markdown output mode. Provides the `cup` binary.

## Requirements

1. **Add `@krodak/clickup-cli` to the node/22 package set**
   - Add `"@krodak/clickup-cli"` to `node/22/node-packages.json`
   - Run `direnv exec . pls gen:node:22` to regenerate `node-packages.nix`, `composition.nix`, and `node-env.nix`
   - The package must resolve and build successfully

2. **Expose the `cup` binary in `node/22/export.nix`**
   - Add `clickup_cli_raw = n."@krodak/clickup-cli";` to the export set
   - Add `clickup_cli` as a `trivialBuilders.writeShellScriptBin` wrapper (sets `${nodejs}/bin` on PATH, calls the `cup` binary)
   - Follow the same pattern as `sg` and `happy_coder` in the same file

3. **Add to CI build job**
   - Add `.#clickup_cli` to the `nix shell` command in `.github/workflows/ci.yaml` build job
   - Add `cup --version &&` to the version verification block

4. **Verify the build**
   - `direnv exec . nix build .#clickup_cli` must succeed
   - The resulting binary must be executable as `cup`

## Constraints

- Use the existing node/22 pattern (node2nix + composition.nix + export.nix)
- Do NOT use the binWrapper pattern (that is for pre-built binaries only)
- Do NOT remove or change any existing packages
- Commit message must follow convention: `new(clickup-cli): Add @krodak/clickup-cli for ClickUp task management`

## Out of Scope

- Committing (handled by the kagent commit agent)
- Adding to `nix/registry.nix` (only if explicitly requested)
- PR creation (handled by the kagent polish phase)
