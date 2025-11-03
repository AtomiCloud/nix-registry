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

For each of the 4 platforms, you need to:

1. Download the binary/archive
2. Calculate its SHA256 hash
3. Convert to Nix format (base64 with `sha256-` prefix)

Use this script pattern:

```bash
# For a direct binary download:
nix-prefetch-url --type sha256 <url>

# For a tarball/zip:
nix-prefetch-url --type sha256 --unpack <url>
```

**Important**: Run this for ALL 4 platforms:

```bash
# Example for garden 0.13.50
nix-prefetch-url --type sha256 --unpack https://download.garden.io/core/0.13.50/garden-0.13.50-linux-amd64.tar.gz
nix-prefetch-url --type sha256 --unpack https://download.garden.io/core/0.13.50/garden-0.13.50-linux-arm64.tar.gz
nix-prefetch-url --type sha256 --unpack https://download.garden.io/core/0.13.50/garden-0.13.50-macos-amd64.tar.gz
nix-prefetch-url --type sha256 --unpack https://download.garden.io/core/0.13.50/garden-0.13.50-macos-arm64.tar.gz
```

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

1. **Wrong SHA**: If you get a hash mismatch, Nix will show you the expected hash - use that.
2. **Platform naming**: Check the actual download URLs to determine correct platform strings.
3. **Archive format**: Use `--unpack` for tar.gz/zip, omit for raw binaries.
4. **Universal binaries**: Some projects (like mirrord) use universal binaries for macOS - both darwin platforms use the same SHA.

## Adding to default.nix

After creating/updating the wrapper, ensure it's imported in `default.nix`:

```nix
gardenio = pkgs.callPackage ./binWrapper/gardenio.nix { inherit nixpkgs; };
```
