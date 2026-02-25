{ nixpkgs }:
with nixpkgs;
let
  inherit (stdenv.hostPlatform) system;
  throwSystem = throw "Unsupported system: ${system}";

  plat = {
    x86_64-linux = "linux-x64";
    aarch64-linux = "linux-arm64";
    x86_64-darwin = "darwin-x64";
    aarch64-darwin = "darwin-arm64";
  }.${system} or throwSystem;

  sha256 = {
    x86_64-linux = "sha256-X87wVqXBjaZaPz/ER/ExyV5EqZqE7IudlRF2cOFL04A=";
    aarch64-linux = "sha256-XDmdcQHoEBqq1Z6OGrRa96/oXRNpCnPr4P0pa40wPEo=";
    x86_64-darwin = "sha256-daJTcCypYbZGqQa2Z3BshaeoxrdtMxTJSUXgDbILu/4=";
    aarch64-darwin = "sha256-s5A/FDmEQ28kGyQniV5entMAdjeLIR+/oC/DiJocw6k=";
  }.${system} or throwSystem;
in
let version = "0.3.6"; in

stdenv.mkDerivation (finalAttrs: {
  pname = "coderabbit";
  inherit version;

  src = fetchurl {
    url = "https://cli.coderabbit.ai/releases/${version}/coderabbit-${plat}.zip";
    inherit sha256;
  };

  nativeBuildInputs = [ unzip ];

  unpackPhase = ''
    unzip $src
  '';

  buildPhase = "true";

  installPhase = ''
    mkdir -p $out/bin
    cp coderabbit $out/bin/coderabbit
    chmod +x $out/bin/coderabbit
  '';

  meta = with lib; {
    description = "CodeRabbit CLI for AI-powered code review";
    longDescription = ''
      CodeRabbit CLI is a command-line tool for AI-powered code review.
      It integrates with Git workflows to provide automated code review
      suggestions using advanced AI models.
    '';
    mainProgram = "coderabbit";
    homepage = "https://coderabbit.ai/";
    downloadPage = "https://github.com/coderabbitai/coderabbit-cli/releases";
    license = licenses.mit;
    platforms = [ "x86_64-linux" "x86_64-darwin" "aarch64-linux" "aarch64-darwin" ];
  };
})
