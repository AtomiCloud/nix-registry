{ nixpkgs }:
with nixpkgs;
let
  inherit (stdenv.hostPlatform) system;
  throwSystem = throw "Unsupported system: ${system}";

  plat = {
    x86_64-linux = "linux";
    aarch64-linux = "linux-arm64";

    x86_64-darwin = "macos";
  }.${system} or throwSystem;


  sha256 = {
    x86_64-linux = "0f7aadde579ebde1443ad2f977beada703f562997fdda603f213faf2a8559868";
    aarch64-linux = "a26e29f6c9480a1226a850c57f80bc79f0ea0c9e59e6440530577bd3d11fef2f";

    x86_64-darwin = "1627507cf5b4d2f7c86247428cc2b6d02fbfa6aa380847cd047a33949d3bdbe1";
  }.${system} or throwSystem;
in
let version = "v10.4.0"; in

stdenv.mkDerivation (finalAttrs: {
  pname = "codecov";
  inherit version;

  src = builtins.fetchurl {
    url = "https://cli.codecov.io/${version}/${plat}/codecov";
    inherit sha256;
  };

  # Disable unpackPhase since we're dealing with a single binary file
  unpackPhase = "true";

  # We don't need to build anything
  buildPhase = "true";

  installPhase = ''
    mkdir -p $out/bin
    cp $src $out/bin/codecov
    chmod +x $out/bin/codecov
  '';

  meta = with lib; {
    description = "Codecov CLI tool for code coverage reporting";
    longDescription = ''
      Codecov CLI is a command-line tool that enables developers to upload code
      coverage reports to Codecov.io. It supports various CI/CD platforms and
      testing frameworks, helping teams track and improve their test coverage
      over time. The tool simplifies the process of integrating code coverage
      metrics into development workflows.
    '';
    mainProgram = "codecov";
    homepage = "https://codecov.io/";
    downloadPage = "https://github.com/codecov/codecov-cli/releases";
    license = licenses.mit;
    platforms = [ "x86_64-linux" "x86_64-darwin" "aarch64-linux" ];
  };
})
