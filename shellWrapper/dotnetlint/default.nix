{ trivialBuilders, nixpkgs }:

let
  # Version of the dotnetlint tool
  version = "0.1.0";

  # Create a function that takes dotnetPackage as an argument
  makeDotnetLint = { dotnetPackage ? nixpkgs.dotnetPackage or nixpkgs.dotnet-sdk_8 }:
    trivialBuilders.writeShellScriptBin {
      name = "dotnetlint";
      inherit version;
      text = ''
        #!/usr/bin/env bash
        set -euo pipefail

        # Handle version flag
        if [[ "$*" == *"--version"* ]]; then
          echo "dotnetlint version ${version}"
          echo "using dotnet: $(${dotnetPackage}/bin/dotnet --version)"
          exit 0
        fi
    
        # Add dotnet to PATH
        export PATH="${dotnetPackage}/bin:$PATH"
        
        # Execute the script
        ${./dotnetlint.sh}
      '';
    };
in
# Make the function overridable, which adds the override attribute
nixpkgs.lib.makeOverridable makeDotnetLint { }
