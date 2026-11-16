{ nixpkgs-go127 }:

# Go 1.27.4 for obs ONLY. The exported registry `go` (golang/go/default.nix) stays 1.26.10, so
# no consumer moves.
#
# obs links VictoriaLogs v1.54.0, whose go.mod says `go 1.27.4` (VictoriaMetrics v1.156.0 says
# 1.27.3). No nixpkgs the registry pins carries Go 1.27, and the registry's own pattern
# (override the 26.05 `go` source to the new version) fails: the 1.26 patch set does not apply
# to 1.27 (MEASURED 16 Nov 2026: go_no_vendor_checks-1.26.patch hunk FAILED).
#
# So the `nixpkgs-go127` flake input (a narrow, exact-revision pin, like `nixpkgs-dotnet`)
# supplies `go_1_27` (1.27.3, with the 1.27 patch set); this overrides its source to 1.27.4.
# Only this file reads that input. Drop both when the registry's main nixpkgs carries
# Go >= 1.27.4.
let
  version = "1.27.4";
in
(nixpkgs-go127.go_1_27.overrideAttrs (
  finalAttrs: _previousAttrs: {
    inherit version;
    src = nixpkgs-go127.fetchurl {
      url = "https://go.dev/dl/go${finalAttrs.version}.src.tar.gz";
      hash = "sha256-c8+4VxrsDAEXdofbhxY9Rqog6XeWcncokaoXZU2aYJk=";
    };
  }
)).overrideAttrs
  (_finalAttrs: _previousAttrs: {
    # An `overrideAttrs` that sets `version` renames the derivation whether or not the new
    # source was really used; this asserts the BUILT BINARY reports the version (same guard as
    # golang/go/default.nix).
    doInstallCheck = true;
    installCheckPhase = ''
      runHook preInstallCheck
      "$out/bin/go" version | grep -Fw "go${version}"
      runHook postInstallCheck
    '';
  })
