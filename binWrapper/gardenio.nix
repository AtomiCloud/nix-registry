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
    x86_64-linux = "sha256:06fv422y4580rvh12cdl72ngm5a0ff6pjg347kmmha18775j418h";
    aarch64-linux = "sha256:1n9apliadjmhjkda2f6x83z9s5h39fdm4gla5ahi82a7g1rzylx0";

    x86_64-darwin = "sha256:01ylmyvb4z0i0kb39ry0nzccz0h4g9cjxcqyxb9zclsj1gghb1b4";
    aarch64-darwin = "sha256:1cj9xl4i6sq28vwmfhi6fdgp2d31q919x6lcrg9axbq6w294nfrj";
  }.${system} or throwSystem;
in
let version = "0.14.13"; in

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
