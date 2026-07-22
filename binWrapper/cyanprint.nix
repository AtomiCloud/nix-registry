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

  # The upstream Linux binary is dynamically linked and its ELF interpreter
  # points at a Nix-store glibc that is not otherwise in this package's closure,
  # so it fails with "cannot execute: required file not found" on any clean
  # machine (e.g. CI runners). autoPatchelfHook rewrites the interpreter/RPATH to
  # the glibc + libgcc we depend on here, pulling them into the runtime closure.
  # Both are Linux-only (glibc/autoPatchelfHook don't exist on Darwin, whose
  # Mach-O binary needs no patching), so guard them behind isLinux.
  nativeBuildInputs = lib.optionals stdenv.hostPlatform.isLinux [ autoPatchelfHook ];
  buildInputs = lib.optionals stdenv.hostPlatform.isLinux [ stdenv.cc.cc.lib glibc ];

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
