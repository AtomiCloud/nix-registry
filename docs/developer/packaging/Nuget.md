# NuGet/.NET Package Packaging Guide

This document explains how to add .NET global tools from NuGet to the nix-registry.

## When to Use

Use NuGet packages when:

- Adding .NET global tools (dotnet tools)
- The tool is available on NuGet.org as a .NET tool package
- You need to run .NET command-line utilities in a Nix environment

## File Structure

```
nuget/
  default.nix    # All package definitions in one file
  common.nix     # Shared build template (creates wrapper with dotnet runtime)
```

All NuGet packages are defined in a single `default.nix` file, using the shared `common.nix` build template.

## How It Works

The NuGet packaging system works in three stages:

1. **fetchNuGet**: Downloads the NuGet package from NuGet.org
2. **DLL Extraction**: Creates a derivation that locates the main `.dll` file
3. **Wrapper Creation**: Creates a shell script that invokes the DLL with the dotnet runtime

### The common.nix Build Template

The `common.nix` template accepts the following parameters:

| Parameter | Required | Description                                   |
| --------- | -------- | --------------------------------------------- |
| `nixpkgs` | Yes      | The nixpkgs instance to use                   |
| `runtime` | Yes      | Either `dotnet-sdk` or `dotnet-runtime`       |
| `name`    | Yes      | The package name (matches NuGet package name) |
| `version` | Yes      | The version of the package                    |
| `sha256`  | Yes      | The SHA256 hash of the NuGet package          |
| `meta`    | No       | Package metadata (license, description, etc.) |

### Runtime Selection

Choose the appropriate runtime based on the tool's requirements:

| Runtime          | Use When                                                   |
| ---------------- | ---------------------------------------------------------- |
| `dotnet-sdk`     | Tool needs SDK features (compilation, EF migrations, etc.) |
| `dotnet-runtime` | Tool only needs the runtime (most CLI tools)               |

**Examples:**

- `dotnet-ef` uses `dotnet-sdk` because it performs compilations
- `dotnetsay` uses `dotnet-runtime` because it's a simple CLI tool

## Required Fields

Each package entry in `nuget/default.nix` requires:

```nix
<package-name> = buildNuget {
  inherit nixpkgs;
  runtime = dotnet-sdk | dotnet-runtime;  # Choose based on tool requirements
  name = "<nuget-package-name>";            # Must match the .dll name
  version = "<version>";                    # Version from NuGet.org
  sha256 = "<hash>";                        # SHA256 hash of the package
  meta = { };                               # Metadata (can be extended)
};
```

## SHA256 Handling

### Getting the Correct Hash

1. **Set to fake hash initially:**

   ```nix
   sha256 = lib.fakeSha256;
   ```

2. **Build the package:**

   ```bash
   nix build .#<package-name>
   ```

3. **Copy the correct hash from the error message:**
   The build will fail with an error like:

   ```
   error: hash mismatch in file downloaded from 'https://www.nuget.org/api/v2/package/...':
   got:    sha256-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=
   wanted: sha256-Qw4Z54Sh4JauWtVHY2lV15CHgGTS/pUKTYjPY7EmDkCk=
   ```

4. **Update with the correct hash:**
   ```nix
   sha256 = "sha256-Qw4Z54Sh4JauWtVHY2lV15CHgGTS/pUKTYjPY7EmDkCk=";
   ```

### Using lib.fakeSha256

You can use `lib.fakeSha256` from nixpkgs:

```nix
sha256 = lib.fakeSha256;
```

Or use the literal string:

```nix
sha256 = "0000000000000000000000000000000000000000000000000000";
```

## Adding a New Package

### Step 1: Find Package Information

1. Go to [NuGet.org](https://www.nuget.org/)
2. Search for your package
3. Note the exact package name and version
4. Check if the tool requires SDK features or just runtime

### Step 2: Add Entry to default.nix

Edit `nuget/default.nix` and add a new entry:

```nix
{ nixpkgs ? import <nixpkgs> { } }:
with nixpkgs;
let buildNuget = import ./common.nix; in
{
  # ... existing packages ...

  my-new-tool = buildNuget {
    inherit nixpkgs;
    runtime = dotnet-runtime;  # or dotnet-sdk if needed
    name = "my-new-tool";
    version = "1.0.0";
    sha256 = lib.fakeSha256;
    meta = {
      description = "Description of the tool";
      homepage = "https://example.com/my-tool";
      license = lib.licenses.mit;
    };
  };
}
```

### Step 3: Get the Correct Hash

```bash
# Build to get hash error
nix build .#my-new-tool

# Copy the correct hash from the error and update the file
# Then rebuild to verify
nix build .#my-new-tool
```

### Step 4: Verify the Package Works

```bash
# Test that the binary runs
nix shell .#my-new-tool -c my-new-tool --version

# Or if the tool doesn't have --version
nix shell .#my-new-tool -c my-new-tool --help
```

### Step 5: Add to CI Verification

Update `.github/workflows/ci.yaml`:

1. Add the package to the `nix shell` command:

   ```yaml
   nix shell nixpkgs#bash
   .#my-new-tool
   # ... other packages ...
   ```

2. Add the verification command:
   ```yaml
   -c bash -c '
   my-new-tool --version &&
   # ... other commands ...
   '
   ```

## Example Template

Here's a complete example for adding a new NuGet package:

```nix
# In nuget/default.nix

dotnet-format = buildNuget {
  inherit nixpkgs;
  runtime = dotnet-sdk;  # Requires SDK for formatting operations
  name = "dotnet-format";
  version = "5.1.250801";
  sha256 = "sha256-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=";  # Will be replaced
  meta = {
    description = "Code formatter for .NET";
    homepage = "https://github.com/dotnet/format";
    license = lib.licenses.mit;
    platforms = lib.platforms.linux ++ lib.platforms.darwin;
  };
};
```

## Complete Example: dotnet-ef

```nix
dotnet-ef = buildNuget {
  inherit nixpkgs;
  runtime = dotnet-sdk;  # Needs SDK for migrations
  name = "dotnet-ef";
  version = "6.0.2";
  sha256 = "sha256-Qw4Z54Sh4JauWtVHY2lV15CHgGTS/pUKTYjPY7EmDkCk=";
  meta = { };
};
```

## Common Issues

### DLL Not Found

**Symptom:** Error like "Could not find 'toolname.dll'"

**Solution:** Ensure the `name` field matches the actual DLL name in the package. The template looks for `{name}.dll`.

### Runtime Issues

**Symptom:** Tool crashes or fails with missing assembly errors

**Solution:** Switch from `dotnet-runtime` to `dotnet-sdk` if the tool needs SDK features.

### Platform Compatibility

**Symptom:** Build fails on certain architectures

**Solution:** Add platform constraints to meta:

```nix
meta = {
  platforms = lib.platforms.linux ++ lib.platforms.darwin;
};
```

### Hash Mismatch After Update

**Symptom:** Previous hash no longer works

**Solution:** NuGet packages may be republished. Always fetch a fresh hash using `lib.fakeSha256`.

## CI Integration

After adding a NuGet package, add it to CI verification:

1. Add to the nix shell command in `.github/workflows/ci.yaml`
2. Add a verification command

See [CI Verification](./CIVerification.md) for detailed instructions.

### Verification Commands by Tool Type

| Tool Type     | Command Pattern    | Example                      |
| ------------- | ------------------ | ---------------------------- |
| Standard tool | `<name> --version` | `dotnet-ef --version`        |
| Simple tool   | `<name>`           | `dotnetsay`                  |
| Help only     | `<name> --help`    | Some tools only support help |

## Related Documentation

- [CI Verification](./CIVerification.md) - Adding packages to CI
