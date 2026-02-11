# Shell Wrapper Packages

This document explains how to create shell script wrapper packages using the `shellWrapper` directory in the nix-registry.

## When to Use

Use shellWrapper packages when you need to:

- Create aliases for existing tools (e.g., `pls` as an alias for `go-task`)
- Wrap commands with default arguments
- Combine multiple tools into a single command
- Add custom behavior or validation around existing tools
- Create linters or formatters that wrap other utilities

## Overview

Shell wrappers use the `trivialBuilders.writeShellScriptBin` function to create lightweight executable scripts that wrap or compose other tools. These are simpler than full derivations and ideal for small utility scripts.

## File Structure

Create your wrapper in the `shellWrapper/<name>/default.nix` directory:

```
shellWrapper/
  default.nix           # Imports all wrappers
  trivialBuilders.nix   # Builder functions
  pls/
    default.nix         # Simple alias wrapper
  dotnetlint/
    default.nix         # Complex wrapper with external script
    dotnetlint.sh       # Shell script implementation
  helmlint/
    default.nix
    helmlint.sh
```

## Builder Function

The `trivialBuilders.writeShellScriptBin` function creates an executable shell script:

```nix
trivialBuilders.writeShellScriptBin {
  name = "my-wrapper";
  version = "1.0.0";
  text = ''
    # Shell script content here
  '';
};
```

### Parameters

| Parameter | Required | Description                                           |
| --------- | -------- | ----------------------------------------------------- |
| `name`    | Yes      | The name of the executable (appears in `/bin/<name>`) |
| `version` | Yes      | Version string for the wrapper                        |
| `text`    | Yes      | The shell script content                              |

## Patterns

### Pattern 1: Simple Alias

Create a simple alias that passes all arguments to another tool:

```nix
{ trivialBuilders, nixpkgs }:

with nixpkgs;
let version = go-task.version; in
{
  myalias = trivialBuilders.writeShellScriptBin {
    name = "myalias";
    inherit version;
    text = ''
      ${go-task}/bin/go-task "$@"
    '';
  };
}
```

**When to use**: When you just need to rename a tool or create an alternative invocation.

### Pattern 2: Wrapper with Multiple Outputs

Create multiple related aliases in one file:

```nix
{ trivialBuilders, nixpkgs }:

with nixpkgs;
let version = go-task.version; in
{
  pls = trivialBuilders.writeShellScriptBin {
    name = "pls";
    inherit version;
    text = ''
      ${go-task}/bin/go-task "$@"
    '';
  };
  please = trivialBuilders.writeShellScriptBin {
    name = "please";
    inherit version;
    text = ''
      ${go-task}/bin/go-task "$@"
    '';
  };
}
```

**When to use**: When you want multiple names for the same underlying tool.

### Pattern 3: Overridable Wrapper

Create a wrapper that allows overriding the underlying package:

```nix
{ trivialBuilders, nixpkgs }:

let
  version = "0.1.0";

  makeMyWrapper = { toolPackage ? nixpkgs.some-tool }:
    trivialBuilders.writeShellScriptBin {
      name = "my-wrapper";
      inherit version;
      text = ''
        #!/usr/bin/env bash
        set -euo pipefail

        if [[ "$*" == *"--version"* ]]; then
          echo "my-wrapper version ${version}"
          echo "using: ${toolPackage}/bin/some-tool"
          exit 0
        fi

        export PATH="${toolPackage}/bin:$PATH"
        ${./my-wrapper.sh}
      '';
    };
in
nixpkgs.lib.makeOverridable makeMyWrapper { }
```

**When to use**: When you want users to be able to customize which underlying tool is used.

### Pattern 4: Wrapper with Dependencies

Create a wrapper that requires multiple tools in PATH:

```nix
{ trivialBuilders, nixpkgs }:

trivialBuilders.writeShellScriptBin {
  name = "my-tool";
  version = "0.2.0";
  text = ''
    export PATH="${nixpkgs.lib.makeBinPath [ nixpkgs.dotnet-sdk_8 nixpkgs.jq nixpkgs.coreutils ]}:$PATH"
    ${./my-tool.sh} "$@"
  '';
}
```

**When to use**: When your script needs multiple tools available in PATH.

### Pattern 5: Version from Wrapped Tool

Derive version from the wrapped package:

```nix
{ trivialBuilders, nixpkgs }:

with nixpkgs;
let version = go-task.version; in
trivialBuilders.writeShellScriptBin {
  name = "pls";
  inherit version;
  text = ''
    ${go-task}/bin/go-task "$@"
  '';
}
```

**When to use**: When you want the wrapper version to match the underlying tool.

## Required Fields

Every shell wrapper must have:

1. **name**: The executable name
2. **version**: A version string (can be derived from wrapped tool or hardcoded)
3. **text**: The shell script content

## Adding Dependencies

### Using runtimeInputs Pattern (Alternative Builder)

The `trivialBuilders.writeShellApplication` function supports `runtimeInputs`:

```nix
trivialBuilders.writeShellApplication {
  name = "my-tool";
  version = "1.0.0";
  runtimeShell = nixpkgs.runtimeShell;
  runtimeInputs = [ nixpkgs.curl nixpkgs.jq ];
  text = ''
    curl -s "https://api.example.com" | jq .
  '';
}
```

### Using lib.makeBinPath

For the simpler `writeShellScriptBin`, add dependencies manually:

```nix
text = ''
  export PATH="${nixpkgs.lib.makeBinPath [ nixpkgs.curl nixpkgs.jq ]}:$PATH"
  # Your script here
'';
```

## External Script Files

For complex scripts, keep the implementation in a separate `.sh` file:

```nix
# default.nix
{ trivialBuilders, nixpkgs }:

trivialBuilders.writeShellScriptBin {
  name = "my-wrapper";
  version = "0.1.0";
  text = ''
    #!/usr/bin/env bash
    set -euo pipefail

    if [[ "$*" == *"--version"* ]]; then
      echo "my-wrapper version 0.1.0"
      exit 0
    fi

    export PATH="${nixpkgs.some-tool}/bin:$PATH"
    ${./my-wrapper.sh}
  '';
}
```

The external script file (`my-wrapper.sh`) contains the main logic:

```bash
#!/usr/bin/env bash
# my-wrapper.sh

# Main implementation here
some-tool --special-args "$@"
```

## Template

Here is a complete template for a new shell wrapper:

```nix
# shellWrapper/my-wrapper/default.nix
{ trivialBuilders, nixpkgs }:

let
  # Version of the wrapper (use wrapped tool's version or hardcode)
  version = "0.1.0";

  # Create an overridable wrapper
  makeMyWrapper = { toolPackage ? nixpkgs.target-tool }:
    trivialBuilders.writeShellScriptBin {
      name = "my-wrapper";
      inherit version;
      text = ''
        #!/usr/bin/env bash
        set -euo pipefail

        # Handle version flag
        if [[ "$*" == *"--version"* ]]; then
          echo "my-wrapper version ${version}"
          echo "using: ${toolPackage}/bin/target-tool"
          exit 0
        fi

        # Add dependencies to PATH
        export PATH="${toolPackage}/bin:$PATH"

        # Run the tool
        ${toolPackage}/bin/target-tool --special-args "$@"
      '';
    };
in
nixpkgs.lib.makeOverridable makeMyWrapper { }
```

## Importing the Wrapper

Add your wrapper to `shellWrapper/default.nix`:

```nix
{ nixpkgs, trivialBuilders }:
with nixpkgs;
with (import ./pls/default.nix { inherit trivialBuilders nixpkgs; });
rec {
  inherit pls please;
  my-wrapper = import ./my-wrapper/default.nix { inherit trivialBuilders nixpkgs; };
}
```

## Testing

### Build the Package

```bash
nix build .#my-wrapper
```

### Test Execution

```bash
# Run directly
nix run .#my-wrapper -- --version

# Or enter a shell with the package
nix shell .#my-wrapper -c my-wrapper --version
```

### Debug Build Issues

```bash
# Show detailed build output
nix build .#my-wrapper --show-trace
```

## CI Integration

After creating your wrapper, add it to CI verification:

1. Add `.#<name>` to the `nix shell` command in `.github/workflows/ci.yaml`
2. Add `<name> --version` to the verification commands

Example:

```yaml
- name: Nix Build
  run: >-
    nix shell nixpkgs#bash
    .#my-wrapper
    # ... other packages
    -c bash -c '
    my-wrapper --version &&
    # ... other commands
    '
```

See [CI Verification](./CIVerification.md) for complete details.

## Best Practices

1. **Always handle `--version`**: CI verification requires a way to check the package works
2. **Use `set -euo pipefail`**: Ensures scripts fail loudly on errors
3. **Quote variables**: Use `"$@"` and `"$var"` to handle spaces correctly
4. **Keep it simple**: If the wrapper becomes complex, consider a full derivation
5. **Document special behavior**: If the wrapper transforms behavior, document it
6. **Make overridable**: Allow users to customize the underlying tool when appropriate

## Related Documentation

- [CI Verification](./CIVerification.md) - Adding packages to CI
