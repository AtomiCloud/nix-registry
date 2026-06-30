{ trivialBuilders, nixpkgs }:

# dn-inspect: AtomiCloud wrapper around JetBrains inspectcode (.NET code inspection).
# NOTE: distinct from the unrelated 'inspect' CLI (Ataraxy-Labs) in binWrapper/inspect.nix.

let
  # Version of the dn-inspect tool (keep in sync with dn-inspect.sh).
  version = "0.3.0";

  # Create a function that takes dotnetPackage as an argument so the .NET SDK is
  # configurable (mirrors shellWrapper/dotnetlint/default.nix). Defaults to .NET 10.
  makeDnInspect = { dotnetPackage ? nixpkgs.dotnetPackage or nixpkgs.dotnet-sdk_10 }:
    trivialBuilders.writeShellScriptBin {
      name = "dn-inspect";
      inherit version;
      text = ''
        export PATH="${nixpkgs.lib.makeBinPath [ dotnetPackage nixpkgs.jq nixpkgs.coreutils nixpkgs.findutils ]}:$PATH"
        export DN_INSPECT_DOTNET="${dotnetPackage}"
        ${./dn-inspect.sh} "$@"
      '';
    };
in
# Make the function overridable, which adds the override attribute.
nixpkgs.lib.makeOverridable makeDnInspect { }
