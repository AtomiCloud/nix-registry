{ nixpkgs }:

# Go, at the version the registry guarantees rather than the version the pinned
# nixpkgs channel happens to carry.
#
# This derivation lives HERE, in the registry, and not in a template. That is R14 /
# D7 RESOLVER-SHAPE: templates compose through the cyanprint nix resolver, which
# merges plain attribute lists, and an `overrideAttrs` is not resolver-mergeable.
# Complexity is hoisted to the registry so a consuming template can keep a plain
# `go` entry.
#
# CVE protection for GO-2026-5856 is therefore this version plus `govulncheck`
# evidence, NOT a per-template override.
let
  version = "1.26.5";
in
(nixpkgs.go.overrideAttrs (
  finalAttrs: _previousAttrs: {
    inherit version;
    src = nixpkgs.fetchurl {
      url = "https://go.dev/dl/go${finalAttrs.version}.src.tar.gz";
      hash = "sha256-SVvkvIcXasVnOS5bQRar2YRm0z17SdQedkzMaXay3EI=";
    };
  }
)).overrideAttrs
  (_finalAttrs: _previousAttrs: {
    # An `overrideAttrs` that sets `version` renames the derivation whether or not
    # the new source was really used, so the attribute name alone cannot witness
    # the toolchain that got built. This asserts the BUILT BINARY reports the
    # version, which is the only claim consumers actually depend on: if the
    # override ever silently resolved to the channel's Go, this build fails here
    # rather than shipping a package whose name says 1.26.5 and whose bytes do not.
    doInstallCheck = true;
    installCheckPhase = ''
      runHook preInstallCheck
      "$out/bin/go" version | grep -Fw "go${version}"
      runHook postInstallCheck
    '';
  })
