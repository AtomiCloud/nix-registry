# Task Specification: Add ccc to Nix registry (CU-86ewrbfra)

## Source

- Ticket: CU-86ewrbfra
- System: ClickUp
- URL: https://app.clickup.com/t/86ewrbfra

## Objective

Add `ccc` (Claude Code Companion) as a Go package to the nix-registry using `buildGoModule`. This is a CLI tool that allows remote control of Claude Code sessions via Telegram.

## Acceptance Criteria

- [x] Package builds successfully with `nix build .#ccc`
- [x] Binary runs correctly with `nix shell .#ccc -c ccc --help`
- [x] Package added to CI verification workflow
- [x] Commit follows convention: `new(ccc): chat with claude code on telegram`

## Definition of Done

- [x] All acceptance criteria met
- [x] No lint errors (nix fmt passes)
- [x] Ticket ID included in commit message
- [x] PR created and ready for review

## Out of Scope

- Adding `ccc` to `nix/registry.nix` (dev shell registry) - not needed
- Building for platforms without pre-built binaries (darwin-x64, linux-arm64)
- Creating multiple version variants

## Technical Constraints

- Must use `buildGoModule` pattern (not binWrapper)
- Must use `vendorHash` with SRI format
- Package location: `golang/ccc/default.nix`
- Version: v1.6.2

## Context

**About ccc:**

- Repository: https://github.com/kidandcat/ccc
- License: MIT
- Language: Go
- Description: Chat with Claude Code on Telegram - allows remote control of Claude Code sessions

**Latest Release (v1.6.2):**

- Published: 2026-02-18
- Bug Fix: extractLastTurn now handles flat JSONL format from Claude Code v2.1.45+

## Technical Decisions

| Decision            | Choice        | Reasoning                                                     |
| ------------------- | ------------- | ------------------------------------------------------------- |
| Package type        | buildGoModule | User requested building from source for full platform support |
| Package location    | `golang/ccc/` | Following existing pattern for Go packages                    |
| Add to registry.nix | No            | Not needed for dev shells, just a buildable package           |
| Version             | v1.6.2        | Latest release at time of implementation                      |

## Implementation Steps (from Golang Package Guide)

1. **Create directory structure**

   ```bash
   mkdir -p golang/ccc
   ```

2. **Create package definition** (`golang/ccc/default.nix`)
   - Use `buildGoModule` pattern
   - Set placeholder hashes initially
   - Configure `doCheck = false` (CLI tool, may require network)

3. **Import in default.nix**
   Add to the `golang` section in `/default.nix`

4. **Generate correct hashes**

   ```bash
   pls gen:go:sha -- ccc ccc/default
   pls gen:go:vendor:sha -- ccc ccc/default
   ```

5. **Test the build**

   ```bash
   nix build .#ccc
   nix shell .#ccc -c ccc --help
   ```

6. **Add to CI verification**
   - Add to `.github/workflows/ci.yaml` nix shell command
   - Add version/help check command

## Package Template

```nix
{ nixpkgs }:
with nixpkgs;
buildGoModule rec {
  pname = "ccc";
  version = "v1.6.2";

  meta = {
    owner = "kidandcat";
    repo = "ccc";
  };

  src = fetchurl {
    url = "https://github.com/${meta.owner}/${meta.repo}/archive/refs/tags/${version}.tar.gz";
    sha256 = "<to-be-generated>";
  };

  vendorHash = "<to-be-generated>";

  doCheck = false;

  ldflags = [ "-w" "-s" "-a" ];
}
```

## Edge Cases

- **Hash mismatch**: Use `pls gen:go:sha` and `pls gen:go:vendor:sha` to regenerate
- **Network access in tests**: Already setting `doCheck = false`

## Error Handling

- Build failure: Check hashes are correct using pls commands
- Missing dependencies: vendorHash should handle Go module dependencies
