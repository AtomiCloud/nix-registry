# Plan 1: Add inspect binWrapper package

## Overview

Add Ataraxy-Labs/inspect v0.1.1 as a binWrapper package in the nix-registry. This creates `binWrapper/inspect.nix` and wires it into `default.nix`. The key difference from simpler binWrapper packages (e.g., `codecov.nix`) is that inspect's pre-built binaries dynamically link to OpenSSL 3 and zlib, requiring `autoPatchelfHook` on Linux and `install_name_tool` on macOS to rewrite library paths.

This addresses all functional and non-functional requirements from the spec.

## Changes

### 1. Create `binWrapper/inspect.nix`

New file following the existing binWrapper pattern (see `codecov.nix`, `mirrord.nix`) with these additions for dynamic library patching:

- **Platform mapping** (`plat` attrset): `x86_64-linux` → `linux-x86_64`, `aarch64-linux` → `linux-aarch64`, `x86_64-darwin` → `macos-x86_64`, `aarch64-darwin` → `macos-aarch64`.
- **SHA256 hashes** from the spec (hex format, matching `codecov.nix` style).
- **Fetch**: `builtins.fetchurl` (consistent with `codecov.nix` for single binary downloads).
- **Linux patching**: Add `autoPatchelfHook` to `nativeBuildInputs` and `openssl`, `zlib`, `stdenv.cc.cc.lib` to `buildInputs`. `autoPatchelfHook` runs automatically during fixup to resolve `libssl.so.3`, `libcrypto.so.3`, `libz.so.1`, `libgcc_s.so.1`. Use conditional `lib.optionals stdenv.hostPlatform.isLinux [...]` so macOS builds don't pull these in.
- **macOS patching**: In `postInstall`, use `install_name_tool -change` to rewrite the two hardcoded Homebrew OpenSSL paths to `${openssl.out}/lib/libssl.3.dylib` and `${openssl.out}/lib/libcrypto.3.dylib`. Guard with `lib.optionalString stdenv.hostPlatform.isDarwin`. The macOS binary already references system `/usr/lib/libz.1.dylib` which is available on all macOS systems, so zlib patching is unnecessary on Darwin.
- **Unpack/build phases**: `unpackPhase = "true"` and `buildPhase = "true"` (single binary, no archive).
- **Install phase**: `mkdir -p $out/bin && cp $src $out/bin/inspect && chmod +x $out/bin/inspect`.
- **`throwSystem`** for unsupported platforms (standard pattern).
- **Meta**: `pname = "inspect"`, `version = "0.1.1"`, `mainProgram = "inspect"`, `homepage = "https://inspect.ataraxy-labs.com"`, `downloadPage = "https://github.com/Ataraxy-Labs/inspect/releases"`, `license = licenses.free` (FSL-1.1-ALv2 has no nixpkgs identifier), `platforms` covering all 4 targets.

### 2. Modify `default.nix` (line ~30)

Add one line to the `bin` attrset:

```nix
inspect = import ./binWrapper/inspect.nix { inherit nixpkgs; };
```

This follows the pattern of `codecov`, `coderabbit`, and `cliproxyapi` which only pass `nixpkgs`.

### 3. Modify `.github/workflows/ci.yaml` (build job, lines ~38-95)

The CI build job explicitly lists every package in a `nix shell` command and smoke-tests each one. Two additions needed:

- Add `.#inspect` to the `nix shell` package list (after the existing `.#dn-inspect` or similar alphabetical position — note: `inspect` and `dn-inspect` are different packages; `dn-inspect` is a .NET tool).
- Add `inspect --help &&` to the bash smoke-test block inside the `-c bash -c '...'` section.

This ensures CI catches build regressions for the inspect package on both `amd64` and `arm64` Linux runners.

## Spec Adherence

| Spec Requirement                                                                | Covered                                           |
| ------------------------------------------------------------------------------- | ------------------------------------------------- |
| FR1: New file `binWrapper/inspect.nix` with v0.1.1 download for all 4 platforms | Yes — primary deliverable                         |
| FR2: Platform mapping (nix system → binary suffix)                              | Yes — `plat` attrset                              |
| FR3: SHA256 hashes per platform                                                 | Yes — `sha256` attrset with spec-provided hashes  |
| FR4: Dynamic library patching (autoPatchelfHook + install_name_tool)            | Yes — conditional per-platform patching           |
| FR5: Binary installation (copy + chmod)                                         | Yes — installPhase                                |
| FR6: `default.nix` integration                                                  | Yes — one import line                             |
| FR7: Meta attributes                                                            | Yes — all specified fields                        |
| NFR1: Linting (nixpkgs-fmt)                                                     | Yes — verified via `nix fmt`                      |
| NFR2: Building (`nix build .#inspect`)                                          | Yes — primary validation                          |
| CI: Build smoke test in `.github/workflows/ci.yaml`                             | Yes — `.#inspect` added to nix shell + smoke test |
| NFR8: Invariant checking (throwSystem)                                          | Yes — standard pattern                            |
| NFR9: Security (HTTPS + SHA256)                                                 | Yes — inherent in fetchurl                        |

NFR3-7, NFR10-12 are marked "does not apply" in the spec and are not addressed.

## Acceptance Criteria

### Functional Checks

1. `nix build .#inspect` succeeds on the current platform (macOS aarch64).
2. `result/bin/inspect --help` or `result/bin/inspect --version` runs without "Library not loaded" or "cannot open shared object file" errors, producing valid CLI output.
3. `binWrapper/inspect.nix` exists and follows the structural pattern of `codecov.nix` (same argument signature `{ nixpkgs }`, same phase structure).
4. `default.nix` contains `inspect = import ./binWrapper/inspect.nix { inherit nixpkgs; };` in the `bin` attrset.
5. `.github/workflows/ci.yaml` build job includes `.#inspect` in the `nix shell` list and `inspect --help &&` in the smoke-test block.

### Non-Functional Checks

1. `nix fmt` produces no changes to `binWrapper/inspect.nix` (linting/formatting).
2. SHA256 hashes match the spec values (binary integrity).
3. Unsupported systems hit `throwSystem` (invariant checking).

## Validation Approach

**Immediate automated checks (dev loop)**:

- Run `direnv exec . nix build .#inspect` — must exit 0.
- Run `direnv exec . ./result/bin/inspect --help` (or `--version`) — must exit 0 and produce output.
- Run `direnv exec . nix fmt` — must produce no diff on `binWrapper/inspect.nix`.
- Verify `binWrapper/inspect.nix` exists and `default.nix` contains the import line.
- Verify `.github/workflows/ci.yaml` contains `.#inspect` and `inspect --help` in the build job.

**Post-release checks**: None (per triage validation matrix).

**Manual checks**: None (per triage validation matrix).
