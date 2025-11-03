{ nixpkgs }:
with nixpkgs;
let
  inherit (stdenv.hostPlatform) system;
  throwSystem = throw "Unsupported system: ${system}";

  plat = {
    x86_64-linux = "linux-amd64";
    aarch64-linux = "linux-arm64";

    x86_64-darwin = "macos-amd64";
    aarch64-darwin = "macos-arm64";
  }.${system} or throwSystem;

  archive_fmt = "tar.gz";

  sha256 = {
    x86_64-linux = "sha256-0knY9h430qaaGM7GKCmGVD7+r2IEPOTQFyrfEedyFSs=";
    aarch64-linux = "sha256-gjWo7PtAC/Vjsk7JZFn8p07CE7qAR2UZD0MihelS6jQ=";

    x86_64-darwin = "sha256-lTeWagHdE+OTINtbXxnZS2Uhhp9SaYyaqa/z0+2caQg=";
    aarch64-darwin = "sha256-NaoFJsO196EoFvUNNTm2FhZid+s/21+2t2Kcyj05/9o=";
  }.${system} or throwSystem;
in
let version = "0.14.9"; in

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
    platforms = [ "x86_64-linux" "x86_64-darwin" "aarch64-darwin" "aarch64-linux" ];
  };
})
