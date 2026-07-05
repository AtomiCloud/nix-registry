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
    x86_64-linux = "sha256-G4MKSJtsEgeKdcMP/sa7V22iNf7dhcaCCIQKqC8QAo8=";
    aarch64-linux = "sha256-cENa8v8S5+iy3m32gTuwxpqo38OasU/SlSkZnYqllGo=";

    aarch64-darwin = "sha256-YDMl0rZQwCfg4aBRgRd/cGwQHfkWcsp/aGs9ZjwAmgo=";
  }.${system} or throwSystem;
in
let version = "4.7.0"; in

stdenv.mkDerivation (finalAttrs: {
  pname = "cyanprint";
  inherit version;

  # The release archive unpacks flat (no top-level directory), unlike
  # gardenio's, so stdenv can't infer a sourceRoot to cd into.
  sourceRoot = ".";

  installPhase = ''
    mkdir -p $out/bin
    cp cyanprint $out/bin/cyanprint
    chmod +x $out/bin/cyanprint
  '';

  src = builtins.fetchurl {
    url = "https://github.com/AtomiCloud/sulfone.lite/releases/download/v${version}/cyanprint_${version}_${plat}.tar.gz";
    inherit sha256;
  };

  meta = with lib; {
    description = "AtomiCloud template tool";
    longDescription = ''
      cyanprint is AtomiCloud's project templating CLI, published by sulfone.lite as
      versioned, per-platform prebuilt binaries.
    '';
    mainProgram = "cyanprint";
    homepage = "https://github.com/AtomiCloud/sulfone.lite";
    downloadPage = "https://github.com/AtomiCloud/sulfone.lite/releases";
    license = licenses.mit;
    platforms = [ "x86_64-linux" "aarch64-linux" "aarch64-darwin" ];
  };
})
