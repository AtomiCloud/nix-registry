{ trivialBuilders, nixpkgs }:

let
  # Version of the helmlint tool
  version = "0.1.0";

  # Create a function that takes helmPackage as an argument
  makeHelmLint = { helmPackage ? nixpkgs.kubernetes-helm or nixpkgs.helm }:
    trivialBuilders.writeShellScriptBin {
      name = "helmlint";
      inherit version;
      text = ''
        #!/usr/bin/env bash
        set -euo pipefail

        # Handle version flag
        if [[ "$*" == *"--version"* ]]; then
          echo "helmlint version ${version}"
          echo "using helm: $(${helmPackage}/bin/helm version --short)"
          exit 0
        fi
    
        # Add helm to PATH
        export PATH="${helmPackage}/bin:$PATH"
        
        # Execute the script
        ${./helmlint.sh}
      '';
    };
in
nixpkgs.lib.makeOverridable makeHelmLint { }
