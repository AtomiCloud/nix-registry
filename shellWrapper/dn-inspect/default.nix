{ trivialBuilders, nixpkgs, dotnetPackage }:

# dn-inspect: AtomiCloud wrapper around JetBrains inspectcode (.NET code inspection).
# NOTE: distinct from the unrelated 'inspect' CLI (Ataraxy-Labs) in binWrapper/inspect.nix.

let
  # Version of the dn-inspect tool (keep in sync with dn-inspect.sh).
  version = "0.4.0";

  # The registry's .NET SDK pin (dotnet/default.nix), bound out of the argument
  # so the function parameter below can keep the name `dotnetPackage`.
  registryDotnet = dotnetPackage;

  # Create a function that takes dotnetPackage as an argument so the .NET SDK is
  # configurable (mirrors shellWrapper/dotnetlint/default.nix). Defaults to the
  # registry pin, which is the same .NET 10 SDK this defaulted to before — the
  # change is that the version is now stated once, in one file, for both .NET
  # wrappers, instead of separately in each.
  makeDnInspect = { dotnetPackage ? registryDotnet }:
    trivialBuilders.writeShellScriptBin {
      name = "dn-inspect";
      inherit version;
      text = ''
        # gawk + gnused are used by dn-inspect.sh (the inspectcode stderr noise
        # filter and quote_arg's single-quote escaping), so bundle them too.
        export PATH="${nixpkgs.lib.makeBinPath [ dotnetPackage nixpkgs.jq nixpkgs.coreutils nixpkgs.findutils nixpkgs.gawk nixpkgs.gnused ]}:$PATH"
        export DN_INSPECT_DOTNET="${dotnetPackage}"
        ${./dn-inspect.sh} "$@"
      '';
    };
in
# Make the function overridable, which adds the override attribute.
nixpkgs.lib.makeOverridable makeDnInspect { }
