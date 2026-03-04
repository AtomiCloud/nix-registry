# Bun Wrapper Packages

This document explains how to create Bun/TypeScript-based wrapper packages using the `bunWrapper` directory in the nix-registry.

## When to Use

Use bunWrapper packages when you need to:

- Create tools that leverage Bun's fast TypeScript execution
- Wrap npm packages with custom TypeScript logic
- Build linters, formatters, or utilities using the Bun runtime
- Create CLI tools that benefit from Bun's native APIs (file system, HTTP, etc.)

Do NOT use bunWrapper when:

- A pre-built binary is available (use binWrapper instead)
- The tool is a simple shell script (use shellWrapper instead)
- The tool needs to run in Node.js specifically (use node packages instead)

## Overview

Bun wrappers use the `trivialBuilders.writeBunScriptBin` function to create executable wrappers around TypeScript source code. Bun handles dependency installation and script execution, making it ideal for fast, self-contained tools.

## File Structure

Create your wrapper in the `bunWrapper/<name>/` directory:

```
bunWrapper/
  default.nix           # Imports all bun wrappers
  trivialBuilders.nix   # Builder functions
  my-tool/
    default.nix         # Package definition
    index.ts            # TypeScript entry point
    package.json        # Dependencies
    bun.lock            # Lockfile (generated, text format)
```

## Builder Function

The `trivialBuilders.writeBunScriptBin` function creates an executable TypeScript wrapper:

```nix
trivialBuilders.writeBunScriptBin {
  name = "my-tool";
  version = "1.0.0";
  src = ./.;
  buildInputs = [ ];  # Optional additional build dependencies
};
```

### Parameters

| Parameter     | Required | Description                                                  |
| ------------- | -------- | ------------------------------------------------------------ |
| `name`        | Yes      | The name of the executable (appears in `/bin/<name>`)        |
| `version`     | Yes      | Version string for the wrapper                               |
| `src`         | Yes      | Source directory containing index.ts, package.json, bun.lock |
| `buildInputs` | No       | Additional packages needed at runtime                        |

## Required Files

Each bunWrapper package directory must contain:

### index.ts

The TypeScript entry point that will be executed:

```typescript
#!/usr/bin/env bun
// index.ts

// Your tool logic here
const args = process.argv.slice(2);
console.log('Hello from bun wrapper!');
```

### package.json

Define your dependencies:

```json
{
  "name": "my-tool",
  "version": "1.0.0",
  "type": "module",
  "dependencies": {
    "some-package": "^1.0.0"
  }
}
```

### bun.lock

Generate the lockfile after adding dependencies:

```bash
cd bunWrapper/my-tool
bun install
```

Note: Modern Bun versions use a text-based `bun.lock` file format.

## Pattern: Complete Package Template

Here is a complete template for a new bun wrapper:

```nix
# bunWrapper/my-tool/default.nix
{ nixpkgs, bun, trivialBuilders }:

let
  version = "1.0.0";
in
trivialBuilders.writeBunScriptBin {
  inherit version;
  name = "my-tool";
  src = ./.;

  # Add any runtime dependencies if needed
  buildInputs = with nixpkgs; [
    # jq
    # curl
  ];
}
```

## Importing the Wrapper

Add your wrapper to `bunWrapper/default.nix`:

```nix
{ nixpkgs, bun }:
let trivialBuilders = import ./trivialBuilders.nix { inherit nixpkgs bun; }; in
{
  my-tool = import ./my-tool { inherit nixpkgs bun trivialBuilders; };
}
```

## Testing

### Build the Package

```bash
nix build .#my-tool
```

### Test Execution

```bash
# Run directly
nix run .#my-tool -- --help

# Or enter a shell with the package
nix shell .#my-tool -c my-tool --help
```

### Debug Build Issues

```bash
# Show detailed build output
nix build .#my-tool --show-trace
```

## CI Integration

After creating your wrapper, add it to CI verification:

1. Add `.#<name>` to the `nix shell` command in `.github/workflows/ci.yaml`
2. Add `<name> --version` (or `--help`) to the verification commands

Example:

```yaml
- name: Nix Build
  run: >-
    nix shell nixpkgs#bash
    .#my-tool
    # ... other packages
    -c bash -c '
    my-tool --version &&
    # ... other commands
    '
```

See [CI Verification](./CIVerification.md) for complete details.

## Comparison with Other Package Types

| Package Type   | Use Case                                  | Runtime |
| -------------- | ----------------------------------------- | ------- |
| `bunWrapper`   | TypeScript tools using Bun                | Bun     |
| `shellWrapper` | Simple shell scripts, aliases             | Shell   |
| `binWrapper`   | Pre-built binaries from vendors           | Native  |
| `node/22`      | Complex Node.js packages with native deps | Node.js |

## Best Practices

1. **Use TypeScript**: Take advantage of type safety for complex tools
2. **Pin dependencies**: Always commit `bun.lock` for reproducible builds
3. **Handle `--version`**: CI verification requires a way to check the package works
4. **Keep dependencies minimal**: Only include what's necessary
5. **Document usage**: Add help output for CLI tools

## Related Documentation

- [CI Verification](./CIVerification.md) - Adding packages to CI
- [ShellWrapper](./ShellWrapper.md) - Shell script wrappers
- [BinWrapper](./BinWrapper.md) - Pre-built binary wrappers
