{ nixpkgs }:
with nixpkgs;
let
  inherit (stdenv.hostPlatform) system;
  throwSystem = throw "Unsupported system: ${system}";

  plat = {
    x86_64-linux = "linux-amd64";
    aarch64-linux = "linux-arm64";

    aarch64-darwin = "macos-arm64";
  }.${system} or throwSystem;

  archive_fmt = "tar.gz";

  sha256 = {
    x86_64-linux = "sha256-g+FISR4Ay1yoMcrrQBo9G8XLjWa5chUK+WoSmreqxQU=";
    aarch64-linux = "sha256-ehlnl2JezQGxPHkICaj/0t58C2M2v/mQuNhbmNNC284=";

    aarch64-darwin = "sha256-iXnkrpdj3p9W1lCPCLDnCXMaGlonzJIu8QQVWF5zDk4=";
  }.${system} or throwSystem;
in
let version = "0.14.20"; in

stdenv.mkDerivation (finalAttrs: {
  pname = "gardenio";
  inherit version;

  installPhase = ''
    mkdir -p $out/bin
    cp garden $out/bin/garden
    chmod +x $out/bin/garden
  '';

  src = builtins.fetchurl {
    url = "https://download.garden.io/core/${version}/garden-${version}-${plat}.tar.gz";
    inherit sha256;
  };

  meta = with lib; {
    description = "garden";
    longDescription = ''
      Automation for Kubernetes development and testing.
    '';
    mainProgram = "garden";
    homepage = "https://garden.io/";
    downloadPage = "https://github.com/garden-io/garden/releases";
    license = licenses.mit;
    platforms = [ "x86_64-linux" "aarch64-darwin" "aarch64-linux" ];
  };
})
