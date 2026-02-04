# deadcode Wrapper

A wrapper for the Go `deadcode` tool (from `golang.org/x/tools/cmd/deadcode`) that fails with exit code 1 when dead code is detected, making it suitable for CI/CD pipelines.

## Overview

The `deadcode` tool finds unused Go code but doesn't fail with a non-zero exit code when issues are found. This wrapper captures the output and exits with code 1 when dead code is detected, enabling reliable CI failures.

## Usage

### Building

```bash
nix build .#deadcode
```

### Running

```bash
# Check a single package
nix run .#deadcode -- ./path/to/go/package

# Check with flags
nix run .#deadcode -- ./... -test=false

# Check specific flags
nix run .#deadcode -- github.com/example/project
```

### In a Nix shell

```bash
nix develop
deadcode ./...
```

## Exit Codes

- **0**: No dead code found
- **1**: Dead code detected (findings are printed to stdout)

## Examples

```bash
# Check entire module
deadcode ./...

# Check specific package
deadcode ./cmd/server

# Check with flags
deadcode ./... -test=false -tags=integration
```

## Deadcode Output

When dead code is found, the output shows the file location and the unused identifier:

```
github.com/example/foo/bar.go:13:6: func unusedFunc is unused
github.com/example/foo/baz.go:20:2: var unusedVar is unused
```

## CI Integration

```yaml
# Example GitHub Actions step
- name: Check for dead code
  run: nix run .#deadcode -- ./...
```

## Version

- Wrapper version: 0.1.0
- Uses Go deadcode from `golang.org/x/tools/cmd/deadcode`
