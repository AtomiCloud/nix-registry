{ nixpkgs }:
with nixpkgs;
let
  inherit (stdenv.hostPlatform) system;
  throwSystem = throw "Unsupported system: ${system}";

  plat = {
    x86_64-linux = "linux_x86_64";
    aarch64-linux = "linux_aarch64";

    x86_64-darwin = "mac_universal";
    aarch64-darwin = "mac_universal";
  }.${system} or throwSystem;

  archive_fmt = "tar.gz";

  sha256 = {
    x86_64-linux = "sha256-GP7n7oTuecRwstUOguaOhHx9HiwNoFO0BSWX2/AB6LI=";
    aarch64-linux = "sha256-WExPypRI9eDpUXoCNkAeO5rsDHULZbpBWLYQxRdvN7I=";

    x86_64-darwin = "sha256-Llui3SXZl15YBtjrr1PbWtyDRBGWdKvMQ8zTqgwgc8s=";
    aarch64-darwin = "sha256-Llui3SXZl15YBtjrr1PbWtyDRBGWdKvMQ8zTqgwgc8s=";
  }.${system} or throwSystem;
in
let version = "3.128.0"; in
let
  binary = fetchurl {
    url = "https://github.com/metalbear-co/mirrord/releases/download/${version}/mirrord_${plat}";
    inherit sha256;
  };
in
stdenv.mkDerivation (finalAttrs: {
  pname = "mirrord";
  inherit version;

  installPhase = ''
    mkdir -p $out/bin
    cp ${binary} $out/bin/mirrord
    chmod +x $out/bin/mirrord
  '';

  postInstall = ''
    chmod +x $out/bin/mirrord
  '';
  src = fetchurl {
    url = "https://github.com/metalbear-co/mirrord/releases/download/${version}/mirrord_${plat}";
    inherit sha256;
  };
  unpackPhase = ":";

  meta = with lib; {
    description = "mirrord CLI for local kubernetes development";
    longDescription = ''
      Connect your local process and your cloud environment, and run local code in cloud conditions.
    '';
    mainProgram = "mirrord";
    homepage = "https://mirrord.dev/";
    downloadPage = "https://github.com/metalbear-co/mirrord/releases";
    license = licenses.mit;
    platforms = [ "x86_64-linux" "x86_64-darwin" "aarch64-darwin" "aarch64-linux" ];
  };
})
