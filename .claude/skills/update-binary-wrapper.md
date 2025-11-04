# Update Binary Wrapper

This skill helps you add or update binary wrapper packages in the `binWrapper/` directory.

## Overview

Binary wrappers are Nix derivations that download and wrap pre-built binaries for multiple platforms. Each wrapper must support 4 architecture + OS combinations:

- `x86_64-linux` (AMD64 Linux)
- `aarch64-linux` (ARM64 Linux)
- `x86_64-darwin` (AMD64 macOS)
- `aarch64-darwin` (ARM64 macOS)

## Process

### 1. Check Latest Version

First, find the latest version from the project's GitHub releases page or download page.

### 2. Determine Download URLs

Identify the URL pattern for the binary downloads. Common patterns:

- `https://github.com/{org}/{repo}/releases/download/{version}/{binary}-{platform}`
- `https://download.{project}.io/{version}/{binary}-{version}-{platform}.tar.gz`

The platform naming varies by project. Common patterns in existing wrappers:

- Garden: `linux-amd64`, `linux-arm64`, `macos-amd64`, `macos-arm64`
- Mirrord: `linux_x86_64`, `linux_aarch64`, `mac_universal`

### 3. Download and Calculate SHA256 for Each Platform

For each of the 4 platforms, you need to download the file and calculate its SHA256 hash.

**IMPORTANT**: Use `nix store prefetch-file` (preferred) or `nix-prefetch-url` WITHOUT `--unpack`. The hash must be of the downloaded file itself, NOT the unpacked contents. `builtins.fetchurl` downloads the file as-is, so we need the hash of the tarball/binary file, not what's inside it.

#### Method 1: Using `nix store prefetch-file` (Recommended)

This command downloads the file and outputs the hash in SRI format directly (ready to use):

```bash
nix store prefetch-file --json <url>
```

**Example for garden 0.14.9** - Run for ALL 4 platforms:

```bash
nix store prefetch-file --json https://download.garden.io/core/0.14.9/garden-0.14.9-linux-amd64.tar.gz
nix store prefetch-file --json https://download.garden.io/core/0.14.9/garden-0.14.9-linux-arm64.tar.gz
nix store prefetch-file --json https://download.garden.io/core/0.14.9/garden-0.14.9-macos-amd64.tar.gz
nix store prefetch-file --json https://download.garden.io/core/0.14.9/garden-0.14.9-macos-arm64.tar.gz
```

The output will include `"hash":"sha256-..."` which you can use directly.

#### Method 2: Using `nix-prefetch-url` (Alternative)

```bash
# Download and get hash (base32 format)
nix-prefetch-url --type sha256 <url>

# Convert to SRI format
nix-hash --to-sri --type sha256 <base32-hash>
```

**CRITICAL**: Do NOT use `--unpack` flag! That hashes the unpacked contents, not the file itself.

#### Method 3: Let Nix Tell You (Fallback)

If the above methods fail, you can put a fake hash in the nix file and let Nix tell you the correct one:

1. Use a fake hash like `sha256-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=`
2. Try to build: `nix build .#package-name`
3. Nix will fail and show you the expected hash (in base32 format)
4. Convert it to SRI: `nix-hash --to-sri --type sha256 <hash-from-error>`

### 4. Update or Create the Nix File

Update the wrapper file in `binWrapper/{package}.nix` with:

- New `version` value
- New `sha256` hashes for all 4 platforms
- Correct `plat` mapping for platform names
- Correct `url` pattern

#### Structure for Archives (tar.gz/zip):

```nix
{ nixpkgs }:
with nixpkgs;
let
  inherit (stdenv.hostPlatform) system;
  throwSystem = throw "Unsupported system: ${system}";

  plat = {
    x86_64-linux = "linux-amd64";
    aarch64-linux = "linux-arm64";
    x86_64-darwin = "macos-amd64";
    aarch64-darwin = "macos-arm64";
  }.${system} or throwSystem;

  sha256 = {
    x86_64-linux = "sha256-...";
    aarch64-linux = "sha256-...";
    x86_64-darwin = "sha256-...";
    aarch64-darwin = "sha256-...";
  }.${system} or throwSystem;
in
let version = "X.Y.Z"; in

stdenv.mkDerivation (finalAttrs: {
  pname = "package-name";
  inherit version;

  installPhase = ''
    mkdir -p $out/bin
    cp binary-name $out/bin/binary-name
    chmod +x $out/bin/binary-name
  '';

  src = builtins.fetchurl {
    url = "https://download.example.com/${version}/package-${version}-${plat}.tar.gz";
    inherit sha256;
  };

  meta = with lib; {
    description = "Short description";
    longDescription = ''
      Longer description.
    '';
    mainProgram = "binary-name";
    homepage = "https://example.com/";
    downloadPage = "https://github.com/org/repo/releases";
    license = licenses.mit;
    platforms = [ "x86_64-linux" "x86_64-darwin" "aarch64-darwin" "aarch64-linux" ];
  };
})
```

#### Structure for Raw Binaries:

```nix
{ nixpkgs }:
with nixpkgs;
let
  inherit (stdenv.hostPlatform) system;
  throwSystem = throw "Unsupported system: ${system}";

  plat = {
    x86_64-linux = "linux_x86_64";
    aarch64-linux = "linux_aarch64";
    x86_64-darwin = "mac_x86_64";
    aarch64-darwin = "mac_aarch64";
  }.${system} or throwSystem;

  sha256 = {
    x86_64-linux = "sha256-...";
    aarch64-linux = "sha256-...";
    x86_64-darwin = "sha256-...";
    aarch64-darwin = "sha256-...";
  }.${system} or throwSystem;
in
let version = "X.Y.Z"; in

stdenv.mkDerivation (finalAttrs: {
  pname = "package-name";
  inherit version;

  installPhase = ''
    mkdir -p $out/bin
    cp $src $out/bin/binary-name
    chmod +x $out/bin/binary-name
  '';

  src = fetchurl {
    url = "https://github.com/org/repo/releases/download/${version}/binary_${plat}";
    inherit sha256;
  };

  unpackPhase = ":";

  meta = with lib; {
    description = "Short description";
    mainProgram = "binary-name";
    homepage = "https://example.com/";
    downloadPage = "https://github.com/org/repo/releases";
    license = licenses.mit;
    platforms = [ "x86_64-linux" "x86_64-darwin" "aarch64-darwin" "aarch64-linux" ];
  };
})
```

### 5. Test the Build

Test that the package builds on your current platform:

```bash
nix build .#package-name
```

### 6. Commit

Use conventional commit format:

- New package: `new(package-name): add package-name vX.Y.Z`
- Update: `update(patch|minor|major)(package-name): update to vX.Y.Z`

## Common Issues

1. **Wrong SHA / Hash Mismatch**: If you get a hash mismatch error, Nix will show you the correct hash in base32 format. Convert it to SRI format using `nix-hash --to-sri --type sha256 <hash>`.

2. **Using `--unpack` flag incorrectly**: DO NOT use `nix-prefetch-url --unpack` for files fetched with `builtins.fetchurl`. The `--unpack` flag hashes the unpacked contents, but `builtins.fetchurl` needs the hash of the archive file itself. Always use `nix store prefetch-file` or `nix-prefetch-url` without `--unpack`.

3. **Platform naming**: Check the actual download URLs to determine correct platform strings. Each project uses different naming conventions (e.g., `linux-amd64` vs `linux_x86_64`).

4. **Universal binaries**: Some projects (like mirrord) use universal binaries for macOS - both darwin platforms use the same SHA in these cases.

## Adding to default.nix

After creating/updating the wrapper, ensure it's imported in `default.nix`:

```nix
gardenio = pkgs.callPackage ./binWrapper/gardenio.nix { inherit nixpkgs; };
```
