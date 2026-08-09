{ nixpkgs, nixpkgs-dotnet }:

# The .NET SDK the registry's .NET tooling is built against, exported under its
# own name.
#
# This exists for the same reason `golang/go` does (R14 / D7 RESOLVER-SHAPE):
# the pin is a registry decision, so it lives HERE and a consuming node keeps a
# plain attribute instead of carrying the pin itself. Before this, every .NET
# node in the fleet re-stated the pin as a per-node
# `atomi.dotnetlint.override { dotnetPackage = pkgs-2605.dotnet-sdk_10; }` — six
# nodes, six identical overrides, and an `.override` is not resolver-mergeable.
#
# `dotnetPackage` is the argument name `dotnetlint` and `dn-inspect` already
# took, kept verbatim so the export and the override knob are one name rather
# than two that have to be kept in agreement.
#
# ## Why this reads a pin of its own, and not `nixpkgs.dotnet-sdk_10`
#
# A .NET repository's `global.json` names an EXACT SDK patch with
# `rollForward: disable`. That is not a preference the SDK can round up from: a
# 10.0.300 SDK in front of a `global.json` asking for 10.0.302 does not warn, it
# refuses to build. So an export whose only guarantee is "whatever .NET 10 the
# registry's channel pin happens to carry today" is not usable as a default — it
# is correct exactly while two independently-moving pins agree, and silently
# breaks the consumer on the first day they do not.
#
# So the version is named here, asserted twice, and taken from an input pinned to
# the exact revision the fleet's .NET nodes pin (see `nixpkgs-dotnet` in
# flake.nix). The registry's own `nixpkgs-2605` keeps moving on the registry's
# cadence and carries 10.0.300 at the time of writing; that is fine, because
# nothing about the .NET SDK reads it any more.
#
# The two assertions are deliberately different in kind:
#
#   * `assert sdk.version == fleetPin` refuses at EVALUATION time. Bumping the
#     `nixpkgs-dotnet` input without bumping `fleetPin` — the drift that would
#     otherwise be invisible — cannot even evaluate.
#   * `dotnet-pin-contract` refuses at BUILD time, by running
#     `dotnet --version` and comparing. The attribute-level `version` is metadata
#     and can be right while the bytes are not; this asserts the claim consumers
#     actually depend on. Same reason `golang/go` install-checks `go version`.
#
# ## The consequence for consumers, stated plainly
#
# Deleting a node's `.override { dotnetPackage = pkgs-2605.dotnet-sdk_10; }` is a
# no-op on the artifact ONLY while that node's own pin resolves `dotnet-sdk_10`
# to `fleetPin`. At the fleet .NET pin it does. At any other pin it does not, and
# the node keeps its override until the two agree — which is what the override
# surface is for, and why it is kept.
let
  # The exact SDK patch the fleet's .NET nodes pin in their global.json.
  # Changing this means changing the `nixpkgs-dotnet` input in flake.nix in the
  # same commit; the assert below is what makes that mandatory rather than
  # remembered.
  fleetPin = "10.0.302";

  sdk = nixpkgs-dotnet.dotnet-sdk_10;
in
assert sdk.version == fleetPin;
{
  dotnetPackage = sdk;

  # Build-time proof that the SDK really reports the pinned patch. `dotnet` wants
  # a writable HOME and would otherwise emit first-run and telemetry noise, so
  # those are turned off rather than tolerated — a version assertion that has to
  # parse around a banner is an assertion waiting to pass by accident.
  dotnet-pin-contract = nixpkgs.runCommand "dotnet-pin-contract" { } ''
    export HOME="$TMPDIR"
    export DOTNET_CLI_TELEMETRY_OPTOUT=1
    export DOTNET_NOLOGO=1
    export DOTNET_SKIP_FIRST_TIME_EXPERIENCE=1

    got=$(${sdk}/bin/dotnet --version)
    echo "==> dotnet --version reports '$got', fleet pin is '${fleetPin}'"
    if [ "$got" != "${fleetPin}" ]; then
      echo "" >&2
      echo "the registry's dotnetPackage does not report the fleet-pinned SDK." >&2
      echo "A .NET node's global.json pins this patch with rollForward: disable," >&2
      echo "so a mismatch is a hard build failure on every node that takes the" >&2
      echo "registry's default instead of overriding. Move the nixpkgs-dotnet" >&2
      echo "input and fleetPin in dotnet/default.nix together." >&2
      exit 1
    fi
    mkdir -p "$out/share/dotnet-pin-contract"
    echo "$got" > "$out/share/dotnet-pin-contract/version"
  '';
}
