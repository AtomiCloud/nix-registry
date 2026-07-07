{ nixpkgs }:
with nixpkgs;
let
  inherit (stdenv.hostPlatform) system;
  throwSystem = throw "Unsupported system: ${system}";

  plat = {
    x86_64-linux = "linux_amd64";
    aarch64-linux = "linux_arm64";

    aarch64-darwin = "darwin_arm64";
  }.${system} or throwSystem;

  sha256 = {
    x86_64-linux = "sha256-suxxRscqokyVkwE1JZ3Wuool/cTa4vySNGKGrHG+oPw=";
    aarch64-linux = "sha256-lNgnALrYUBkSgvKmOVtW6jOmfWCTc3ITD64VwzdHzWo=";

    aarch64-darwin = "sha256-floxGKsUhXS3ZYBdHLoWwzoFlt9Qpl9oeM9j2PF//kA=";
  }.${system} or throwSystem;
in
let version = "0.0.532"; in

stdenv.mkDerivation (finalAttrs: {
  pname = "nsc";
  inherit version;

  # The release archive unpacks flat (no top-level directory), so stdenv can't
  # infer a sourceRoot to cd into.
  sourceRoot = ".";

  installPhase = ''
    mkdir -p $out/bin
    # Main CLI plus the docker/bazel credential helpers shipped alongside it.
    for bin in nsc docker-credential-nsc bazel-credential-nsc; do
      cp "$bin" "$out/bin/$bin"
      chmod +x "$out/bin/$bin"
    done
  '';

  src = builtins.fetchurl {
    url = "https://github.com/namespacelabs/foundation/releases/download/v${version}/nsc_${version}_${plat}.tar.gz";
    inherit sha256;
  };

  meta = with lib; {
    description = "Namespace Cloud CLI";
    longDescription = ''
      nsc is the command-line interface for Namespace Cloud, providing access to
      Namespace's developer-optimized compute platform (remote builds, ephemeral
      environments, CI runners and container registries). Published by
      namespacelabs/foundation as versioned, per-platform prebuilt binaries.
    '';
    mainProgram = "nsc";
    homepage = "https://namespace.so/";
    downloadPage = "https://github.com/namespacelabs/foundation/releases";
    license = licenses.asl20;
    platforms = [ "x86_64-linux" "aarch64-linux" "aarch64-darwin" ];
  };
})
