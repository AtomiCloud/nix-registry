# BinWrapper Package Guide

This document explains how to add pre-built binary packages (binWrapper) to the nix-registry.

## When to Use BinWrapper

Use the binWrapper package type when:

- The tool provides **pre-built binaries** (GitHub releases, vendor downloads)
- You **don't need to compile** from source
- **Multi-platform support** is required (Linux and macOS, AMD64 and ARM64)
- The binary is **standalone** with minimal dependencies

Do NOT use binWrapper when:

- Source code is the only distribution method (use language-specific builders instead)
- The tool requires complex build steps or dependencies
- You need to patch or modify the binary

## File Structure

Create your package file in:

```
binWrapper/<package-name>.nix
```

For example:

- `binWrapper/mirrord.nix`
- `binWrapper/codecov.nix`

## Required Fields

Every binWrapper package must include:

| Field          | Description                | Example                                   |
| -------------- | -------------------------- | ----------------------------------------- |
| `pname`        | Package name               | `"mirrord"`                               |
| `version`      | Version string             | `"3.182.0"` or `"v10.4.0"`                |
| `sha256`       | Platform-specific hashes   | See below                                 |
| `src`          | Download source (fetchurl) | `fetchurl { url = ...; inherit sha256; }` |
| `installPhase` | Installation script        | Copies binary to `$out/bin`               |
| `meta`         | Package metadata           | Description, license, platforms           |

### Optional but Recommended Fields

| Field         | Description               | Example                |
| ------------- | ------------------------- | ---------------------- |
| `unpackPhase` | Override for raw binaries | `":"` or `"true"`      |
| `buildPhase`  | Skip build step           | `"true"`               |
| `postInstall` | Post-installation steps   | Additional chmod, etc. |

## Platform-Specific SHA256 Handling

BinWrapper packages must support all four platforms:

- `x86_64-linux` - AMD64 Linux
- `aarch64-linux` - ARM64 Linux
- `x86_64-darwin` - Intel macOS
- `aarch64-darwin` - Apple Silicon macOS

### Platform Mapping

Different vendors use different naming conventions. Map them using the `plat` attribute:

```nix
plat = {
  x86_64-linux = "linux_x86_64";      # Vendor-specific name
  aarch64-linux = "linux_aarch64";
  x86_64-darwin = "macos_x86_64";
  aarch64-darwin = "macos_aarch64";
}.${system} or throwSystem;
```

### SHA256 per Platform

Define hashes for each platform:

```nix
sha256 = {
  x86_64-linux = "sha256-ABC...";
  aarch64-linux = "sha256-DEF...";
  x86_64-darwin = "sha256-GHI...";
  aarch64-darwin = "sha256-JKL...";
}.${system} or throwSystem;
```

### Universal Binaries

Some projects provide universal binaries (same binary for multiple architectures):

```nix
# Example: macOS universal binary (same for x86_64 and aarch64)
sha256 = {
  x86_64-linux = "sha256-ABC...";
  aarch64-linux = "sha256-DEF...";
  x86_64-darwin = "sha256-UNIVERSAL...";   # Same hash
  aarch64-darwin = "sha256-UNIVERSAL...";  # Same hash
}.${system} or throwSystem;

plat = {
  x86_64-linux = "linux_x86_64";
  aarch64-linux = "linux_aarch64";
  x86_64-darwin = "mac_universal";   # Same platform name
  aarch64-darwin = "mac_universal";  # Same platform name
}.${system} or throwSystem;
```

## Example Templates

### Template 1: Raw Binary from GitHub Releases

For tools that distribute raw binaries (no archive):

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
    x86_64-linux = "sha256-PLACEHOLDER";
    aarch64-linux = "sha256-PLACEHOLDER";
    x86_64-darwin = "sha256-PLACEHOLDER";
    aarch64-darwin = "sha256-PLACEHOLDER";
  }.${system} or throwSystem;
in
let version = "X.Y.Z"; in

stdenv.mkDerivation (finalAttrs: {
  pname = "package-name";
  inherit version;

  src = fetchurl {
    url = "https://github.com/org/repo/releases/download/${version}/binary_${plat}";
    inherit sha256;
  };

  unpackPhase = ":";
  buildPhase = "true";

  installPhase = ''
    mkdir -p $out/bin
    cp $src $out/bin/package-name
    chmod +x $out/bin/package-name
  '';

  meta = with lib; {
    description = "Short description of the package";
    longDescription = ''
      Longer description explaining what the tool does and its use cases.
    '';
    mainProgram = "package-name";
    homepage = "https://example.com/";
    downloadPage = "https://github.com/org/repo/releases";
    license = licenses.mit;
    platforms = [ "x86_64-linux" "x86_64-darwin" "aarch64-darwin" "aarch64-linux" ];
  };
})
```

### Template 2: Binary from Vendor Downloads

For tools distributed from vendor servers:

```nix
{ nixpkgs }:
with nixpkgs;
let
  inherit (stdenv.hostPlatform) system;
  throwSystem = throw "Unsupported system: ${system}";

  plat = {
    x86_64-linux = "linux";
    aarch64-linux = "linux-arm64";
    x86_64-darwin = "macos";
    aarch64-darwin = "macos";
  }.${system} or throwSystem;

  sha256 = {
    x86_64-linux = "sha256-PLACEHOLDER";
    aarch64-linux = "sha256-PLACEHOLDER";
    x86_64-darwin = "sha256-PLACEHOLDER";
    aarch64-darwin = "sha256-PLACEHOLDER";
  }.${system} or throwSystem;
in
let version = "v10.4.0"; in

stdenv.mkDerivation (finalAttrs: {
  pname = "package-name";
  inherit version;

  src = builtins.fetchurl {
    url = "https://cli.vendor.io/${version}/${plat}/binary-name";
    inherit sha256;
  };

  unpackPhase = "true";
  buildPhase = "true";

  installPhase = ''
    mkdir -p $out/bin
    cp $src $out/bin/package-name
    chmod +x $out/bin/package-name
  '';

  meta = with lib; {
    description = "Short description";
    longDescription = ''
      Longer description.
    '';
    mainProgram = "package-name";
    homepage = "https://vendor.io/";
    downloadPage = "https://github.com/vendor/repo/releases";
    license = licenses.mit;
    platforms = [ "x86_64-linux" "x86_64-darwin" "aarch64-linux" "aarch64-darwin" ];
  };
})
```

### Template 3: TAR.GZ Archive

For tools distributed as tar.gz archives (common for Go/Rust projects):

```nix
{ nixpkgs }:
with nixpkgs;
let
  inherit (stdenv.hostPlatform) system;
  throwSystem = throw "Unsupported system: ${system}";

  plat = {
    x86_64-linux = "linux-amd64";
    aarch64-linux = "linux-arm64";
    x86_64-darwin = "darwin-amd64";
    aarch64-darwin = "darwin-arm64";
  }.${system} or throwSystem;

  sha256 = {
    x86_64-linux = "sha256-PLACEHOLDER";
    aarch64-linux = "sha256-PLACEHOLDER";
    x86_64-darwin = "sha256-PLACEHOLDER";
    aarch64-darwin = "sha256-PLACEHOLDER";
  }.${system} or throwSystem;
in
let version = "v1.0.0"; in

stdenv.mkDerivation (finalAttrs: {
  pname = "package-name";
  inherit version;

  src = fetchurl {
    url = "https://github.com/org/repo/releases/download/${version}/package-name_${plat}.tar.gz";
    inherit sha256;
  };

  # tar.gz is auto-extracted, no need to override unpackPhase
  buildPhase = "true";

  installPhase = ''
    mkdir -p $out/bin
    cp package-name $out/bin/package-name
    chmod +x $out/bin/package-name
  '';

  meta = with lib; {
    description = "Short description";
    mainProgram = "package-name";
    homepage = "https://github.com/org/repo";
    license = licenses.mit;
    platforms = [ "x86_64-linux" "x86_64-darwin" "aarch64-linux" "aarch64-darwin" ];
  };
})
```

### Template 4: ZIP Archive

For tools distributed as ZIP archives:

```nix
{ nixpkgs }:
with nixpkgs;
let
  inherit (stdenv.hostPlatform) system;
  throwSystem = throw "Unsupported system: ${system}";

  plat = {
    x86_64-linux = "linux_x86_64";
    aarch64-linux = "linux_aarch64";
    x86_64-darwin = "macos_x86_64";
    aarch64-darwin = "macos_aarch64";
  }.${system} or throwSystem;

  sha256 = {
    x86_64-linux = "sha256-PLACEHOLDER";
    aarch64-linux = "sha256-PLACEHOLDER";
    x86_64-darwin = "sha256-PLACEHOLDER";
    aarch64-darwin = "sha256-PLACEHOLDER";
  }.${system} or throwSystem;
in
let version = "v1.0.0"; in

stdenv.mkDerivation (finalAttrs: {
  pname = "package-name";
  inherit version;

  src = fetchurl {
    url = "https://github.com/org/repo/releases/download/${version}/package-name_${plat}.zip";
    inherit sha256;
  };

  nativeBuildInputs = [ unzip ];

  # ZIP is auto-extracted when unzip is in nativeBuildInputs
  buildPhase = "true";

  installPhase = ''
    mkdir -p $out/bin
    cp package-name $out/bin/package-name
    chmod +x $out/bin/package-name
  '';

  meta = with lib; {
    description = "Short description";
    mainProgram = "package-name";
    homepage = "https://github.com/org/repo";
    license = licenses.mit;
    platforms = [ "x86_64-linux" "x86_64-darwin" "aarch64-linux" "aarch64-darwin" ];
  };
})
```

### Template 5: Nested Binary in Archive

For archives where the binary is in a subdirectory or has a different name:

```nix
{ nixpkgs }:
with nixpkgs;
let
  inherit (stdenv.hostPlatform) system;
  throwSystem = throw "Unsupported system: ${system}";

  plat = {
    x86_64-linux = "linux-amd64";
    aarch64-linux = "linux-arm64";
    x86_64-darwin = "darwin-amd64";
    aarch64-darwin = "darwin-arm64";
  }.${system} or throwSystem;

  sha256 = {
    x86_64-linux = "sha256-PLACEHOLDER";
    aarch64-linux = "sha256-PLACEHOLDER";
    x86_64-darwin = "sha256-PLACEHOLDER";
    aarch64-darwin = "sha256-PLACEHOLDER";
  }.${system} or throwSystem;
in
let version = "v1.0.0"; in

stdenv.mkDerivation (finalAttrs: {
  pname = "package-name";
  inherit version;

  src = fetchurl {
    url = "https://github.com/org/repo/releases/download/${version}/repo_${plat}.tar.gz";
    inherit sha256;
  };

  buildPhase = "true";

  installPhase = ''
    mkdir -p $out/bin
    # Binary might be in a subdirectory or have a different name
    cp bin/actual-binary-name $out/bin/package-name
    chmod +x $out/bin/package-name
  '';

  meta = with lib; {
    description = "Short description";
    mainProgram = "package-name";
    homepage = "https://github.com/org/repo";
    license = licenses.mit;
    platforms = [ "x86_64-linux" "x86_64-darwin" "aarch64-linux" "aarch64-darwin" ];
  };
})
```

## Archive Handling Summary

| Archive Type | Native Build Inputs | Unpack Behavior               |
| ------------ | ------------------- | ----------------------------- |
| Raw binary   | None                | Skip with `unpackPhase = ":"` |
| tar.gz       | None                | Auto-extracted                |
| tar.bz2      | None                | Auto-extracted                |
| tar.xz       | None                | Auto-extracted                |
| ZIP          | `[ unzip ]`         | Auto-extracted                |
| Other        | As needed           | Manual unpackPhase            |

## How to Get Hashes

### Method 1: Using `nix store prefetch-file` (Recommended)

This is the preferred method as it outputs the hash in SRI format directly:

```bash
nix store prefetch-file --json <url>
```

Example for all four platforms:

```bash
# Linux AMD64
nix store prefetch-file --json https://github.com/org/repo/releases/download/v1.0.0/binary_linux_x86_64

# Linux ARM64
nix store prefetch-file --json https://github.com/org/repo/releases/download/v1.0.0/binary_linux_aarch64

# macOS Intel
nix store prefetch-file --json https://github.com/org/repo/releases/download/v1.0.0/binary_mac_x86_64

# macOS Apple Silicon
nix store prefetch-file --json https://github.com/org/repo/releases/download/v1.0.0/binary_mac_aarch64
```

The output will include `"hash": "sha256-..."` which you can copy directly into your nix file.

### Method 2: Using `nix-prefetch-url` (Alternative)

```bash
# Download and get hash (base32 format)
nix-prefetch-url --type sha256 <url>

# Convert to SRI format for use in nix files
nix-hash --to-sri --type sha256 <base32-hash>
```

**CRITICAL**: Do NOT use the `--unpack` flag. That hashes the unpacked contents, not the file itself. Since `builtins.fetchurl` downloads the file as-is, we need the hash of the file, not what's inside it.

### Method 3: Let Nix Tell You (Fallback)

If the above methods fail, use a fake hash and let Nix calculate the correct one:

1. Use a placeholder hash in your nix file:

   ```nix
   sha256 = {
     x86_64-linux = "sha256-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=";
     ...
   };
   ```

2. Try to build:

   ```bash
   nix build .#package-name
   ```

3. Nix will fail with an error showing the correct hash (in base32 format)

4. Convert it to SRI format:
   ```bash
   nix-hash --to-sri --type sha256 <hash-from-error>
   ```

## Importing in default.nix

After creating your package file, add it to `default.nix` in the `bin` section:

```nix
# bin wrapper
bin = rec {
  mirrord = import ./binWrapper/mirrord.nix { inherit nixpkgs; };
  codecov = import ./binWrapper/codecov.nix { inherit nixpkgs; };
  your-package = import ./binWrapper/your-package.nix { inherit nixpkgs; };
};
```

If your package depends on other packages in the registry:

```nix
your-package = import ./binWrapper/your-package.nix {
  inherit nixpkgs;
  other-dep = bin.other-dep;
};
```

## CI Integration

After adding a new package, you must add it to CI verification in `.github/workflows/ci.yaml`.

### Step 1: Add to nix shell command

Add `.#<package-name>` to the `nix shell` command:

```yaml
- name: Nix Build
  run: >-
    nix shell nixpkgs#bash
    .#mirrord
    .#codecov
    .#your-package    # Add your package here
    ...
```

### Step 2: Add verification command

Add the verification command in the bash script:

```yaml
    -c bash -c '
    mirrord --version &&
    codecov --version &&
    your-package --version &&    # Add your command here
    ...
    '
```

### Verification Command Patterns

| Package Type      | Command Pattern      | Example                |
| ----------------- | -------------------- | ---------------------- |
| Standard CLI      | `<name> --version`   | `codecov --version`    |
| Subcommand-based  | `<name> version`     | `garden version`       |
| Help only         | `<name> --help`      | `cli-proxy-api --help` |
| No output options | `<name>`             | `dotnetsay`            |
| Different binary  | `<binary> --version` | `wt --version`         |

## Testing

### Build Test

Test that the package builds correctly:

```bash
nix build .#package-name
```

### Runtime Test

Test that the binary runs correctly:

```bash
nix shell .#package-name -c package-name --version
```

### Local CI Simulation

Test like CI does:

```bash
nix shell .#package1 .#package2 -c bash -c '
package1 --version &&
package2 --version &&
echo "All tests passed!"
'
```

## Common Issues

### Hash Mismatch

**Symptom**: Error message about hash mismatch

**Solution**: Nix will show the expected hash in the error message. Convert it to SRI format:

```bash
nix-hash --to-sri --type sha256 <hash-from-error>
```

### Platform Not Supported

**Symptom**: `Unsupported system: ...`

**Solution**:

1. Check that all four platforms are defined in the `plat` and `sha256` attributes
2. Verify the upstream provides binaries for all platforms

### Wrong Platform Names

**Symptom**: Download URL returns 404

**Solution**: Check the actual download URLs from the project's releases page. Each project uses different naming conventions:

- `linux-amd64` vs `linux_x86_64` vs `linux`
- `macos` vs `darwin` vs `mac`

### Binary Not Executable

**Symptom**: Permission denied when running binary

**Solution**: Ensure `chmod +x` is in the `installPhase`:

```nix
installPhase = ''
  mkdir -p $out/bin
  cp $src $out/bin/package-name
  chmod +x $out/bin/package-name
'';
```

## Existing Examples

| Package  | File                      | Source Type      | Template       |
| -------- | ------------------------- | ---------------- | -------------- |
| mirrord  | `binWrapper/mirrord.nix`  | GitHub releases  | Raw binary     |
| codecov  | `binWrapper/codecov.nix`  | Vendor downloads | Raw binary     |
| gardenio | `binWrapper/gardenio.nix` | Vendor downloads | tar.gz archive |

## Related Documentation

- [CI Verification](./CIVerification.md) - Adding packages to CI
