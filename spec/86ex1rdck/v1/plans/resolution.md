# Resolution: refine_local

## What Went Wrong

Plan 1, Change 2 instructs the implementer to "Run `task gen:node:22` to regenerate `node-packages.nix` and `composition.nix`" but does not mention that node2nix resolves all unpinned packages to their **latest npm versions**. This caused every iteration to silently upgrade existing packages:

- **Iterations 1-3**: `@krodak/clickup-cli` upgraded from 1.8.0 to 1.23.1
- **Iteration 4**: clickup-cli was fixed, but `swagger-typescript-api` (13.6.5→13.6.8) and `happy-coder` (0.13.0→0.13.1) were upgraded instead

This violates the spec's "Out of Scope: Upgrading other existing packages" rule. Reviewer-3 (claude-auto-codex) consistently rejected on this basis across all iterations (70%→85%→88% completion). By iterations 6-7, implementers independently discovered version-pinning and applied it, but the reviewer timed out before producing a verdict.

**Evidence**: kloop run `36re9t7o`, reviewer-3 verdicts in loops 1-4; checkpoint summaries at loops 3 and 6 confirming "reviewer disagreement about node2nix regeneration scope."

## What Needs to Change in Plans

### Plan 1, Change 2 — Add version-pinning pre-step

Before running `task gen:node:22`, the implementer MUST pin all existing packages in `node/22/node-packages.json` to their current installed versions using `"package@version"` syntax. This prevents node2nix from upgrading unrelated packages during regeneration.

Concrete steps to add:

1. **Before modifying `node-packages.json`**, record the current versions of all existing packages from `node/22/node-packages.nix` (grep for version strings of each top-level package).
2. **Pin each existing entry** in `node-packages.json` to its current version. For example:
   - `"@krodak/clickup-cli"` → `"@krodak/clickup-cli@1.8.0"`
   - `"swagger-typescript-api"` → `"swagger-typescript-api@13.6.5"`
   - (apply to all 8 existing entries)
3. **Then** add `"pagerduty-cli"` (unpinned, so it resolves to latest).
4. **Then** run `task gen:node:22`.

### Plan 1, Validation Approach — Add version drift check

Add a validation step: after regeneration, run `git diff node/22/node-packages.nix` and verify that no existing package versions changed. Only new entries for `pagerduty-cli` and its transitive dependencies should appear.

## Which Plans Are Affected

Plan 1 only (there is only one plan).

## Constraints

- The `node-packages.json` format supports `"package@version"` pinning — documented in `docs/developer/packaging/Node.md`.
- The new `pagerduty-cli` entry should remain unpinned (no `@version`) so it resolves to the latest available version.
- Do not modify any other aspect of the plan — the export.nix wrapper, CI changes, and commit format are all correct.
