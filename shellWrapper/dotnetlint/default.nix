{ trivialBuilders, nixpkgs, dotnetPackage }:

let
  # Version of the dotnetlint tool
  version = "0.1.0";

  # The registry's .NET SDK pin (dotnet/default.nix), bound out of the argument
  # so the function parameter below can keep the name `dotnetPackage` without
  # referring to itself.
  registryDotnet = dotnetPackage;

  # Create a function that takes dotnetPackage as an argument.
  #
  # The default is the registry's pin rather than a channel SDK. It used to be
  # `nixpkgs.dotnetPackage or nixpkgs.dotnet-sdk_8` — an attribute nixpkgs does
  # not have, so the fallback was always what ran, and every one of the six .NET
  # nodes in the fleet overrode it to .NET 10. Nothing consumed the .NET 8
  # default; it was a value only reachable by not asking for anything.
  makeDotnetLint = { dotnetPackage ? registryDotnet }:
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
