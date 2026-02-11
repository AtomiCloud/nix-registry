# Python Package Guide

This document explains how to add Python CLI tools to the nix-registry using `buildPythonPackage`.

## When to Use

Use Python packages when:

- Adding Python CLI tools from PyPI
- The tool is distributed as a Python package with dependencies
- The tool is not available in other forms (binary, npm, etc.)

## File Structure

```
python/
  <package-name>/
    default.nix
```

## Required Components

### buildPythonPackage with fetchPypi

Python packages use `buildPythonPackage` from `python3Packages` along with `fetchPypi` to download the source from PyPI.

### Required Fields

| Field                   | Description                             | Example                                                |
| ----------------------- | --------------------------------------- | ------------------------------------------------------ |
| `pname`                 | Package name (must match PyPI name)     | `"aws-export-credentials"`                             |
| `version`               | Version string                          | `"0.13.0"`                                             |
| `format`                | Package format (usually `"setuptools"`) | `"setuptools"`                                         |
| `src`                   | Source derivation using `fetchPypi`     | `fetchPypi { inherit pname version; sha256 = "..."; }` |
| `propagatedBuildInputs` | Runtime dependencies                    | `[ click sh ]`                                         |
| `checkPhase`            | Test phase (often skipped)              | `echo "no test!"`                                      |

## Basic Example

```nix
{ nixpkgs, pyPkgs ? nixpkgs.pkgs.python3Packages }:
with pyPkgs;
buildPythonPackage rec {
  pname = "example-package";
  version = "1.0.0";
  format = "setuptools";
  src = fetchPypi {
    inherit version pname;
    sha256 = "sha256-hash-here";
  };
  checkPhase = ''
    echo "no test!"
  '';
  propagatedBuildInputs = [ click sh ];
}
```

## Handling Nested Dependencies

When a Python package requires a specific version of a dependency that differs from nixpkgs, you need to build that dependency inline. This is done using a `let` binding to define the custom dependency before the main package.

### Example with Nested Dependency (botocore)

The `aws-export-credentials` package requires a specific version of `botocore`. Here's how it's handled:

```nix
{ nixpkgs, pyPkgs ? nixpkgs.pkgs.python3Packages }:
with pyPkgs;
let
  # Define the nested dependency with its specific version
  bc = buildPythonPackage rec {
    pname = "botocore";
    version = "1.29.26";
    format = "setuptools";
    src = fetchPypi {
      inherit version pname;
      sha256 = "f71220fe5a5d393c391ed81a291c0d0985f147568c56da236453043f93727a34";
    };
    checkPhase = ''
      echo "no test!"
    '';
    propagatedBuildInputs = [ urllib3 jmespath python-dateutil docutils pytest ];
  };
in
buildPythonPackage rec {
  pname = "aws-export-credentials";
  version = "0.13.0";
  format = "setuptools";
  src = fetchPypi {
    inherit version pname;
    sha256 = "2051da8b9c3ca9a00557c366f0fbfae2967b360d3d28439fc5b21bef4a20068f";
  };
  checkPhase = ''
    echo "no test!"
  '';
  # Use the custom botocore (bc) instead of the nixpkgs version
  propagatedBuildInputs = [ click sh bc ];
}
```

### When to Use Nested Dependencies

1. **Version conflicts**: The package requires a different version than what nixpkgs provides
2. **Missing dependencies**: The dependency isn't available in nixpkgs
3. **Patches needed**: You need to modify the dependency

### Tips for Nested Dependencies

- Use short variable names (e.g., `bc` for botocore) to keep `propagatedBuildInputs` readable
- Include all transitive dependencies in the nested package's `propagatedBuildInputs`
- Check the package's `setup.py` or `pyproject.toml` for exact version requirements

## sha256 Handling

### Getting the Correct Hash

1. **Set to fake hash initially**:

   ```nix
   sha256 = lib.fakeSha256;
   # or
   sha256 = "sha256-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=";
   ```

2. **Build the package**:

   ```bash
   nix build .#<package-name>
   ```

3. **Copy the correct hash from the error message**:

   ```
   error: hash mismatch in fixed-output derivation '/nix/store/...':
   specified: sha256-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=
   got:    sha256-2051da8b9c3ca9a00557c366f0fbfae2967b360d3d28439fc5b21bef4a20068f
   ```

4. **Update the sha256**:
   ```nix
   sha256 = "2051da8b9c3ca9a00557c366f0fbfae2967b360d3d28439fc5b21bef4a20068f";
   ```

### Hash Format

Use the full SHA256 hash (64 characters), not the base32 format:

- Correct: `"2051da8b9c3ca9a00557c366f0fbfae2967b360d3d28439fc5b21bef4a20068f"`
- Alternative: `"sha256-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="` (SRI format)

## checkPhase for Skipping Tests

Most Python packages in this registry skip tests to speed up builds and avoid test dependency issues:

```nix
checkPhase = ''
  echo "no test!"
'';
```

### When to Run Tests

If you want to run the actual test suite:

```nix
checkPhase = ''
  runHook preCheck
  pytest -v
  runHook postCheck
'';

# Add test dependencies
nativeCheckInputs = [ pytest pytest-mock ];
```

## Complete Example Template

```nix
{ nixpkgs, pyPkgs ? nixpkgs.pkgs.python3Packages }:
with pyPkgs;
# Uncomment and define nested dependencies if needed
# let
#   customDep = buildPythonPackage rec {
#     pname = "custom-dependency";
#     version = "1.0.0";
#     format = "setuptools";
#     src = fetchPypi {
#       inherit version pname;
#       sha256 = "hash-here";
#     };
#     checkPhase = ''
#       echo "no test!"
#     '';
#     propagatedBuildInputs = [ dependency1 dependency2 ];
#   };
# in
buildPythonPackage rec {
  pname = "your-package-name";
  version = "1.0.0";
  format = "setuptools";

  src = fetchPypi {
    inherit version pname;
    sha256 = "sha256-hash-here";
  };

  checkPhase = ''
    echo "no test!"
  '';

  propagatedBuildInputs = [
    # Add runtime dependencies from nixpkgs python3Packages
    click
    sh
    requests
    # Add custom dependencies if defined above
    # customDep
  ];
}
```

## Importing in default.nix

After creating your package, import it in `/default.nix` under the `python` section:

```nix
python = {
  aws-export-credentials = import ./python/aws-export-credentials/default.nix { inherit nixpkgs; };
  your-package-name = import ./python/your-package-name/default.nix { inherit nixpkgs; };
};
```

## CI Integration

Add your package to CI verification in `.github/workflows/ci.yaml`:

### Step 1: Add to nix shell command

```yaml
- name: Nix Build
  run: >-
    nix shell nixpkgs#bash
    .#aws-export-credentials
    .#your-package-name    # Add here
    ...
```

### Step 2: Add verification command

```yaml
    -c bash -c '
    aws-export-credentials --version &&
    your-package-name --version &&    # Add here
    ...
    '
```

### Step 3: Test locally first

```bash
nix shell .#your-package-name -c your-package-name --version
```

## Common Dependencies

These are commonly used Python packages available in `python3Packages`:

| Package           | Usage                   |
| ----------------- | ----------------------- |
| `click`           | CLI framework           |
| `sh`              | Shell command wrapper   |
| `requests`        | HTTP library            |
| `urllib3`         | URL library             |
| `jmespath`        | JSON query              |
| `python-dateutil` | Date utilities          |
| `docutils`        | Documentation utilities |
| `pytest`          | Testing framework       |
| `boto3`           | AWS SDK                 |
| `botocore`        | AWS SDK core            |

## Troubleshooting

### Missing Dependency Error

**Error**: `ModuleNotFoundError: No module named 'xxx'`

**Solution**: Add the missing module to `propagatedBuildInputs`:

```nix
propagatedBuildInputs = [ existing-deps missing-module ];
```

### Version Conflict

**Error**: Package requires specific version of dependency

**Solution**: Define the dependency inline using a `let` binding (see Nested Dependencies section)

### Hash Mismatch

**Error**: `hash mismatch in fixed-output derivation`

**Solution**: This is expected when using `lib.fakeSha256`. Copy the correct hash from the error message.

### Build Fails with Test Errors

**Error**: Tests fail during build

**Solution**: Skip tests with:

```nix
checkPhase = ''
  echo "no test!"
'';
```

### Package Not Found on PyPI

**Error**: `fetchPypi` fails to download

**Solution**:

1. Verify the package name matches PyPI exactly (check https://pypi.org/project/<name>/)
2. Check if the version exists
3. Some packages use different names for the vs distribution (check `src.name`)

## Related Documentation

- [CI Verification](./CIVerification.md) - Adding packages to CI
