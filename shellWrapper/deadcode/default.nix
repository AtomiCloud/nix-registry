{ trivialBuilders, nixpkgs }:

let
  # Version of the deadcode wrapper
  version = "0.1.0";

  # Create a function that takes deadcodePackage as an argument
  # gotools contains the deadcode binary at ${gotools}/bin/deadcode
  makeDeadcode = { deadcodePackage ? nixpkgs.gotools }:
    trivialBuilders.writeShellScriptBin {
      name = "deadcode";
      inherit version;
      text = ''
        #!/usr/bin/env bash
        set -euo pipefail

        # Handle version flag
        if [[ "$*" == *"--version"* ]]; then
          echo "deadcode wrapper version ${version}"
          echo "using: ${deadcodePackage}/bin/deadcode"
          exit 0
        fi

        # Run deadcode and capture output (both stdout and stderr)
        output=$(${deadcodePackage}/bin/deadcode "$@" 2>&1 || true)

        # If there is any output, print it and exit with code 1
        if [ -n "$output" ]; then
          echo "$output"
          exit 1
        fi

        # No deadcode found, exit successfully
        exit 0
      '';
    };
in
nixpkgs.lib.makeOverridable makeDeadcode { }
