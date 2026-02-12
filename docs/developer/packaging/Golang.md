# Golang Package Guide

This document explains how to add and maintain Go packages in the nix-registry.

## When to Use

Use Go packages when:

- Adding Go CLI tools and applications
- Source is available on GitHub or another accessible source
- The application uses Go modules for dependency management

## File Structure

```
golang/
  <name>/
    default.nix
```

Example: `golang/nix-share/default.nix`

## buildGoModule Pattern

Go packages in this registry use `buildGoModule`, which handles Go module dependencies automatically.

### Required Fields

| Field        | Description                                       |
| ------------ | ------------------------------------------------- |
| `pname`      | Package name                                      |
| `version`    | Version string (include `v` prefix if using tags) |
| `src`        | Source archive or directory                       |
| `vendorHash` | Hash of vendored dependencies                     |

### Optional Fields

| Field         | Description                          |
| ------------- | ------------------------------------ |
| `meta.owner`  | GitHub repository owner              |
| `meta.repo`   | GitHub repository name               |
| `doCheck`     | Whether to run tests (default: true) |
| `ldflags`     | Linker flags for build optimization  |
| `subPackages` | Specific packages to build           |

## Hash Handling

### vendorHash vs vendorSha256

- **`vendorHash`**: SRI hash format (preferred) - `sha256-xxxxx`
- **`vendorSha256`**: Legacy base64 format - `sha256:xxxxx`

This registry uses `vendorHash` with the SRI format for consistency.

### Placeholder Hash

When creating a new package, use the placeholder hash:

```nix
vendorHash = "sha256:dpBqw+QbfOh4wg4Sz/qevfTpNlYcMHlg9c7XCfeK1e0=";
```

This is an invalid hash that will cause the build to fail and display the correct hash in the error message.

## Pls Commands

### Module SHA Generation

Calculate the source archive SHA:

```bash
pls gen:go:sha -- <nix-identifier> <path>
```

Examples:

```bash
# For: { nix-share = import ./golang/nix-share/default.nix ... }
pls gen:go:sha -- nix-share nix-share/default

# For versioned: { nix-share_0_1_2 = import ./golang/nix-share/0.1.2.nix ... }
pls gen:go:sha -- nix-share_0_1_2 nix-share/0.1.2
```

### Vendor SHA Generation

Calculate the vendor/dependency hash:

```bash
pls gen:go:vendor:sha -- <nix-identifier> <path>
```

Examples:

```bash
# For: { nix-share = import ./golang/nix-share/default.nix ... }
pls gen:go:vendor:sha -- nix-share nix-share/default
```

## Example Template

```nix
{ nixpkgs }:
with nixpkgs;
buildGoModule rec {
  pname = "<package-name>";
  version = "v1.0.0";

  meta = {
    owner = "<github-owner>";
    repo = "<github-repo>";
  };

  src = fetchurl {
    url = "https://github.com/${meta.owner}/${meta.repo}/archive/refs/tags/${version}.tar.gz";
    sha256 = "<source-sha256>";
  };

  vendorHash = "<vendor-hash>";

  # Disable tests if they require network access or external dependencies
  doCheck = false;

  # Optional: Add linker flags for smaller binaries
  ldflags = [ "-w" "-s" "-a" ];
}
```

## Complete Example

From `golang/nix-share/default.nix`:

```nix
{ nixpkgs }:
with nixpkgs;
buildGoModule rec {
  pname = "nix-share";
  version = "v0.1.2";

  meta = {
    owner = "kirinnee";
    repo = "nix-share";
  };

  src = fetchurl {
    url = "https://github.com/${meta.owner}/${meta.repo}/archive/refs/tags/${version}.tar.gz";
    sha256 = "sha256-eNQqwp6/vT6xDE8UuNz5NQuNOPPpHS7uKSlhQ2wNIO4=";
  };

  vendorHash = "sha256:dpBqw+QbfOh4wg4Sz/qevfTpNlYcMHlg9c7XCfeK1e0=";

  doCheck = false;

  ldflags = [ "-w" "-s" "-a" ];
}
```

## Step-by-Step: Adding a New Go Package

### 1. Create Directory Structure

```bash
mkdir -p golang/<package-name>
```

### 2. Create Package Definition

Create `golang/<package-name>/default.nix` with the template above.

### 3. Set Placeholder Hashes

Use placeholder values:

- Source SHA: `sha256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=`
- Vendor hash: `sha256:dpBqw+QbfOh4wg4Sz/qevfTpNlYcMHlg9c7XCfeK1e0=`

### 4. Import in default.nix

Add to `/default.nix` in the `golang` section:

```nix
golang = {
  nix-share = import ./golang/nix-share/default.nix { inherit nixpkgs; };
  <package-name> = import ./golang/<package-name>/default.nix { inherit nixpkgs; };
};
```

### 5. Generate Correct Hashes

```bash
# First, get the source SHA
pls gen:go:sha -- <package-name> <package-name>/default

# Then, get the vendor hash
pls gen:go:vendor:sha -- <package-name> <package-name>/default
```

### 6. Test the Build

```bash
nix build .#<package-name>
```

### 7. Test the Binary

```bash
nix shell .#<package-name> -c <package-name> --version
```

## CI Integration

After adding a new Go package, add it to CI verification:

### 1. Add to nix shell command

In `.github/workflows/ci.yaml`, add to the `nix shell` command:

```yaml
- name: Nix Build
  run: >-
    nix shell nixpkgs#bash
    .#<package-name>
    ...
```

### 2. Add verification command

Add the version check in the bash script:

```yaml
-c bash -c '
<package-name> --version &&
...
'
```

### 3. Handle Special Cases

Some Go packages may not have `--version`:

- Use `--help` if no version flag exists
- Just run the binary if no output options exist

See [CI Verification](./CIVerification.md) for detailed instructions.

## Common Issues

### Hash Mismatch

**Error**: `hash mismatch in fixed-output derivation`

**Solution**:

1. Run `pls gen:go:sha -- <name> <path>` to get the correct source hash
2. Run `pls gen:go:vendor:sha -- <name> <path>` to get the correct vendor hash

### Network Access in Tests

**Error**: Tests fail due to network access requirements

**Solution**: Set `doCheck = false;` in the package definition

### Multiple Binaries

If a package produces multiple binaries, specify which to build:

```nix
subPackages = [ "cmd/main" ];
```

## Related Documentation

- [CI Verification](./CIVerification.md) - Adding packages to CI
