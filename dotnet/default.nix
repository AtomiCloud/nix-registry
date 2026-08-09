{ nixpkgs }:

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
# It is a plain alias of `nixpkgs.dotnet-sdk_10`, deliberately NOT an
# `overrideAttrs` of it. A node gets the SDK on PATH from its own
# `inherit (pkgs-2605) dotnet-sdk_10` *and* gets the wrappers from here; if this
# were a re-derivation those would be two different store paths for one SDK, and
# `mkShell` silently orders a PATH collision rather than reporting it. Same
# derivation, one path, no collision to order.
{
  dotnetPackage = nixpkgs.dotnet-sdk_10;
}
