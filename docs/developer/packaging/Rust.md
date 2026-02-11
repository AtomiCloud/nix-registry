# Rust Packages

This document explains how to add Rust applications to the nix-registry using the Fenix toolchain.

## When to Use

Use Rust packages when:

- Adding Rust CLI tools and applications
- Source is available on GitHub or another source repository
- The package uses Cargo for dependency management

## File Structure

```
rust/
  default.nix       # Package aggregation
  lib.nix           # Fenix toolchain setup
  <name>/
    default.nix     # Package definition
```

## Fenix Toolchain Integration

The registry uses [Fenix](https://github.com/nix-community/fenix) for Rust toolchain management. The toolchain is configured in `rust/lib.nix`:

```nix
{ nixpkgs, fenix }:
with nixpkgs;
with fenix; {

  rust = with complete.toolchain; combine ([
    stable.cargo
    stable.rustc
    stable.rust-src
    stable.rust-std
    openssl
  ]);

}
```

This creates a combined Rust toolchain with:

- `stable.cargo` - Cargo package manager
- `stable.rustc` - Rust compiler
- `stable.rust-src` - Rust source code
- `stable.rust-std` - Standard library
- `openssl` - OpenSSL for native dependencies

## Package Definition

Rust packages use `buildRustPackage` from the Rust platform created with the Fenix toolchain.

### Required Fields

| Field       | Description                                    |
| ----------- | ---------------------------------------------- |
| `pname`     | Package name                                   |
| `version`   | Package version                                |
| `src`       | Source code (typically from `fetchFromGitHub`) |
| `cargoHash` | Hash of Cargo dependencies                     |

### Common Optional Fields

| Field         | Description                           |
| ------------- | ------------------------------------- |
| `buildInputs` | Native dependencies (e.g., `openssl`) |
| `doCheck`     | Whether to run tests (default: true)  |
| `checkPhase`  | Custom test phase                     |
| `meta`        | Package metadata                      |

## Example Package

Here is the complete example from `rust/toml-cli/default.nix`:

```nix
{ nixpkgs, rust }:
with nixpkgs;
(nixpkgs.makeRustPlatform {
  cargo = rust;
  rustc = rust;
}).buildRustPackage rec {
  pname = "toml-cli";
  version = "0.2.3";

  src = fetchFromGitHub {
    owner = "gnprice";
    repo = pname;
    rev = "v${version}";
    hash = "sha256-/JDgUAjSBCPFUs8E10eD4ZQtWGgV3Bwioiy1jT91E84=";
  };

  buildInputs = ([
    openssl
  ]);

  cargoHash = "sha256-PoqVMTCRmSTt7UhCpMF3ixmAfVtpkaOfaTTmDNhrpLA=";

  doCheck = false;
  checkPhase = "";

  meta = with lib; {
    description = "Simple CLI for editing and querying TOML files";
    longDescription = ''
      Simple CLI for editing and querying TOML files
    '';
    mainProgram = "toml";
    homepage = "https://github.com/gnprice/toml-cli";
    downloadPage = "https://github.com/gnprice/toml-cli/releases";
    license = licenses.mit;
    platforms = [ "x86_64-linux" "x86_64-darwin" "aarch64-darwin" "aarch64-linux" ];
  };

}
```

## Step-by-Step Guide

### 1. Create Package Directory

```bash
mkdir -p rust/<package-name>
```

### 2. Create Package Definition

Create `rust/<package-name>/default.nix`:

```nix
{ nixpkgs, rust }:
with nixpkgs;
(nixpkgs.makeRustPlatform {
  cargo = rust;
  rustc = rust;
}).buildRustPackage rec {
  pname = "<package-name>";
  version = "<version>";

  src = fetchFromGitHub {
    owner = "<owner>";
    repo = pname;
    rev = "v${version}";  # or commit hash
    hash = "";  # Will be filled after first build attempt
  };

  buildInputs = ([
    openssl  # Include if package needs native SSL support
  ]);

  cargoHash = lib.fakeHash;  # Use fake hash initially

  meta = with lib; {
    description = "<description>";
    mainProgram = "<binary-name>";
    homepage = "https://github.com/<owner>/<repo>";
    license = licenses.<license>;
    platforms = [ "x86_64-linux" "x86_64-darwin" "aarch64-darwin" "aarch64-linux" ];
  };
}
```

### 3. Import in rust/default.nix

Add the import to `rust/default.nix`:

```nix
{ nixpkgs, fenix }:
let rust = (import ./lib.nix { inherit nixpkgs fenix; }).rust; in
{
  toml-cli = import ./toml-cli/default.nix { inherit nixpkgs rust; };
  <package-name> = import ./<package-name>/default.nix { inherit nixpkgs rust; };
}
```

### 4. Get Correct Hashes

First build attempt will fail with a hash mismatch error, providing the correct hash:

```bash
nix build .#<package-name>
```

The error message will show:

1. The correct `hash` for the source
2. The correct `cargoHash` for dependencies

Update the package definition with both hashes and rebuild.

### 5. Test the Package

```bash
# Build the package
nix build .#<package-name>

# Test the binary
nix shell .#<package-name> -c <binary-name> --version
```

### 6. Add to CI Verification

Add the package to `.github/workflows/ci.yaml`:

```yaml
- name: Nix Build
  run: >-
    nix shell nixpkgs#bash
    ...
    .#<package-name>
    -c bash -c '
    ...
    <binary-name> --version &&
    ...
    '
```

See [CI Verification](./CIVerification.md) for more details.

## Template

Here is a minimal template for new Rust packages:

```nix
{ nixpkgs, rust }:
with nixpkgs;
(nixpkgs.makeRustPlatform {
  cargo = rust;
  rustc = rust;
}).buildRustPackage rec {
  pname = "<NAME>";
  version = "<VERSION>";

  src = fetchFromGitHub {
    owner = "<OWNER>";
    repo = pname;
    rev = "v${version}";
    hash = "<SOURCE_HASH>";
  };

  buildInputs = ([ openssl ]);

  cargoHash = "<CARGO_HASH>";

  meta = with lib; {
    description = "<DESCRIPTION>";
    mainProgram = "<BINARY>";
    homepage = "https://github.com/<OWNER>/<NAME>";
    license = licenses.mit;
    platforms = [ "x86_64-linux" "x86_64-darwin" "aarch64-darwin" "aarch64-linux" ];
  };
}
```

## Native Dependencies

### Common buildInputs

| Dependency   | Use Case          |
| ------------ | ----------------- |
| `openssl`    | SSL/TLS support   |
| `pkg-config` | Library discovery |
| `zlib`       | Compression       |
| `sqlite`     | SQLite database   |

### Example with Multiple Dependencies

```nix
buildInputs = [
  openssl
  pkg-config
  zlib
];
```

## Disabling Tests

Some packages may have tests that fail in the Nix build environment. Disable them with:

```nix
doCheck = false;
checkPhase = "";
```

## Troubleshooting

### cargoHash Mismatch

**Symptom**: Build fails with hash mismatch error

**Solution**: This is expected on first build. Copy the correct hash from the error message and update `cargoHash`.

### Missing Native Dependencies

**Symptom**: Build fails with "could not find library" or similar errors

**Solution**: Add the missing library to `buildInputs`:

```nix
buildInputs = [ openssl pkg-config ];
```

### Wrong Binary Name

**Symptom**: CI verification fails because binary name differs from package name

**Solution**: Use `mainProgram` in meta to specify the correct binary name:

```nix
meta = {
  mainProgram = "actual-binary-name";
};
```

### Platform-Specific Issues

**Symptom**: Build fails on specific architecture

**Solution**: Check upstream support and adjust `meta.platforms`:

```nix
platforms = [ "x86_64-linux" "x86_64-darwin" ];  # Omit unsupported platforms
```

## Related Documentation

- [CI Verification](./CIVerification.md) - Adding packages to CI
