# Spec: Package inspect (Ataraxy-Labs) to nix-registry

## Summary

Add Ataraxy-Labs/inspect v0.1.1 as a binWrapper package in the nix-registry. Inspect is an entity-level code review CLI for Git with graph-based risk scoring. Unlike the simpler binWrapper packages, the pre-built binaries dynamically link to OpenSSL 3 and zlib, requiring `autoPatchelfHook` on Linux and `install_name_tool` on macOS to rewrite library paths.

## Verification Evidence

1. **"Standalone executables with no external runtime dependencies"** — **DENIED**.
   - Downloaded `inspect-macos-aarch64` from v0.1.1 release. `otool -L` shows:
     - `/opt/homebrew/opt/openssl@3/lib/libssl.3.dylib`
     - `/opt/homebrew/opt/openssl@3/lib/libcrypto.3.dylib`
     - `/usr/lib/libz.1.dylib`
     - System frameworks (Security, CoreFoundation, libiconv, libSystem)
   - Downloaded `inspect-linux-x86_64`. `readelf -d` shows:
     - `libssl.so.3`, `libcrypto.so.3`, `libz.so.1`, `libgcc_s.so.1`, `libm.so.6`, `libc.so.6`
   - Running the macOS binary fails with: `Library not loaded: /opt/homebrew/opt/openssl@3/lib/libssl.3.dylib`
   - **Impact**: Must patch library references, not a simple copy-and-chmod.

2. **"Binary naming convention stable across releases"** — **PARTIALLY VERIFIED**.
   - v0.1.1 assets: `inspect-linux-aarch64`, `inspect-linux-x86_64`, `inspect-macos-aarch64`, `inspect-macos-x86_64`, `inspect-windows-x86_64.exe` (confirmed via `gh release view v0.1.1`).
   - v0.1.0 has **no assets** (`gh release view v0.1.0 --json assets` returns empty array). Cannot compare naming across versions.
   - Naming is stable within v0.1.1. Low risk for future updates since the pattern is standard.

**Additional finding**: The project is a Rust codebase (`Cargo.toml`, `Cargo.lock` at root), not Python as GitHub's language detection reports. The OpenSSL dependency is typical for Rust binaries using the `openssl` crate.

## Requirements

### Functional Requirements

1. **New file `binWrapper/inspect.nix`** that downloads the v0.1.1 pre-built binary from `https://github.com/Ataraxy-Labs/inspect/releases/download/v0.1.1/inspect-{platform}` for all 4 platforms: `x86_64-linux`, `aarch64-linux`, `x86_64-darwin`, `aarch64-darwin`.

2. **Platform mapping**: Nix system identifiers map to binary suffixes:
   - `x86_64-linux` → `linux-x86_64`
   - `aarch64-linux` → `linux-aarch64`
   - `x86_64-darwin` → `macos-x86_64`
   - `aarch64-darwin` → `macos-aarch64`

3. **SHA256 hashes**: Per-platform hashes from GitHub release digest field (to be verified with `nix-prefetch-url` during implementation):
   - `x86_64-linux`: `99cf4ea2a2a1048d8e9369a6a5a11e5f84ee3f3c706e0bde072f9b2bd44e96ba`
   - `aarch64-linux`: `2327c1de10ecf40e5199c15fdc4c4b3c173735640294e779c635f4c15771e4f6`
   - `x86_64-darwin`: `51751be22f6128229c5dea30dc54e8816b81eb90b53d42b318b11b3afee831d2`
   - `aarch64-darwin`: `e7fed5722af6e14dc668279dd7854109f9778484d48b1a42ead5d2c71b8bb90d`

4. **Dynamic library patching**:
   - **Linux**: Use `autoPatchelfHook` with `openssl`, `zlib`, and `stdenv.cc.cc.lib` as `buildInputs` to automatically fix the dynamic linker and shared library paths.
   - **macOS**: Use `install_name_tool` in `postInstall` to rewrite the hardcoded Homebrew OpenSSL paths (`/opt/homebrew/opt/openssl@3/lib/libssl.3.dylib` and `/opt/homebrew/opt/openssl@3/lib/libcrypto.3.dylib`) to the Nix store `openssl` derivation paths.

5. **Binary installation**: Copy the downloaded binary to `$out/bin/inspect` and `chmod +x`.

6. **`default.nix` integration**: Add `inspect = import ./binWrapper/inspect.nix { inherit nixpkgs; };` to the `bin` attrset in `default.nix`.

7. **Meta attributes**: Include `pname`, `version`, `description`, `mainProgram = "inspect"`, `homepage = "https://inspect.ataraxy-labs.com"`, `downloadPage`, `platforms`, and `license`. Use `licenses.free` — the actual license is FSL-1.1-ALv2 (Functional Source License, converts to Apache 2.0 after 2 years) but there's no nixpkgs identifier for it, so treat it as free.

### Non-Functional Requirements

1. **Linting** — Applies. The new `.nix` file must pass `nixpkgs-fmt` (the project formatter in `nix/fmt.nix`). No new lint rules needed.

2. **Building** — Applies. `nix build .#inspect` must succeed. The derivation must correctly fetch the binary and patch dynamic library references. No impact on build time of other packages.

3. **Unit Testing** — Does not apply. Nix package definitions don't have unit tests in this repository. Build verification serves as the functional test.

4. **Integration Testing** — Does not apply. This is an isolated new package with no interaction with other packages (unlike `infrautils` which depends on `gardenio` and `mirrord`).

5. **End-to-End Testing** — Does not apply. No user-facing UI. The binary's own `--help`/`--version` output serves as a smoke test.

6. **Documentation** — Does not apply. The project doesn't maintain per-package documentation. The commit message and flake output serve as documentation.

7. **Observability** — Does not apply. This is a local development tool, not a deployed service.

8. **Invariant Checking** — Applies minimally. The derivation should `throw` on unsupported systems (standard pattern: `throwSystem`). SHA256 hashes enforce binary integrity at build time.

9. **Security** — Low concern. The binary is fetched over HTTPS with SHA256 verification.

10. **Performance** — Does not apply. No runtime performance impact. Download size is ~40-48MB per platform (one-time fetch, cached by Nix store).

11. **Backwards Compatibility** — Does not apply. New package, no existing consumers.

12. **Accessibility** — Does not apply. CLI tool, no UI.

**Additional domain-specific item**:

- **Library patching correctness**: The `autoPatchelfHook` approach on Linux is well-established in nixpkgs. The `install_name_tool` approach on macOS is standard for rewriting Homebrew paths. Both must be tested by actually running the patched binary.

## Acceptance Criteria

1. `nix build .#inspect` succeeds on the current platform (macOS aarch64).
2. The built binary runs: `result/bin/inspect --help` (or `--version`) produces valid output without library-not-found errors.
3. `binWrapper/inspect.nix` follows the existing binWrapper patterns in the repository (consistent structure with `codecov.nix`, `mirrord.nix`, etc.).
4. `default.nix` includes `inspect` in the `bin` attrset.
5. The nix file passes formatting (`nix fmt` produces no changes).

## Out of Scope

- Adding `inspect` to `nix/registry.nix` (the dev shell registry) — only add if explicitly requested.
- Packaging older or future versions of inspect.
- Building inspect from source (Rust cargo build) — we use the pre-built binaries.
- Windows platform support (Windows binaries exist but Nix doesn't target Windows).
- Verifying inspect's actual code review functionality — we only verify it launches.
