{ nixpkgs, bun }:
with nixpkgs;
let
  version = "1.0.0";

  # Immutable source pin: tag v1.0.0 of the standalone repository.
  src = fetchFromGitHub {
    owner = "AtomiCloud";
    repo = "releaser";
    rev = "3200bdd95a0fdd8f43f9905faa8c85afe4595d1f"; # v1.0.0
    hash = "sha256-L8kPuS1uc0RPOk9eqrvYGjtKqq780Tdcy7o42df6eyo=";
  };

  # Production deps only: the compiled binary bundles runtime imports (all pure
  # JS), and excluding devDependencies (notably the platform-specific
  # @biomejs/biome binary) keeps node_modules identical across linux/darwin so
  # one fixed-output hash suffices.
  deps = stdenv.mkDerivation {
    pname = "releaser-deps";
    inherit version src;
    nativeBuildInputs = [ bun ];
    dontConfigure = true;
    buildPhase = ''
      export HOME="$TMPDIR"
      bun install --frozen-lockfile --no-progress --production
    '';
    installPhase = ''
      mkdir -p "$out"
      cp -r node_modules "$out/node_modules"
    '';
    dontFixup = true;
    outputHashMode = "recursive";
    outputHashAlgo = "sha256";
    outputHash = "sha256-QS0fN9OiIBGKAxxWCsvZBP9hmErPoL5vWN6NG6lbfPc=";
  };
in
stdenv.mkDerivation {
  pname = "releaser";
  inherit version src;

  nativeBuildInputs = [ bun ];
  dontConfigure = true;

  buildPhase = ''
    export HOME="$TMPDIR"
    cp -r --no-preserve=mode ${deps}/node_modules ./node_modules
    bun build ./bin/releaser.ts --compile --outfile releaser
  '';

  installPhase = ''
    install -Dm755 releaser "$out/bin/releaser"
  '';

  # Bun --compile emits a self-contained executable; normal fixup strip would
  # corrupt the embedded bytecode blob.
  dontFixup = true;

  meta = with lib; {
    description = "AtomiCloud offline-first release and commit-lint CLI";
    longDescription = ''
      releaser is AtomiCloud's offline-first conventional release and
      commit-lint automation CLI. It owns the whole release pipeline (version
      calculation, release notes, changelog, conventions doc, tags, GitHub
      releases) and commit-message linting from one atomi_release.yaml
      configuration, replacing sg (semantic-generator) and python gitlint.
      Built from the pinned v1.0.0 source tag with Bun's single-file compiler.
    '';
    mainProgram = "releaser";
    homepage = "https://github.com/AtomiCloud/releaser";
    license = licenses.mit;
    platforms = [ "x86_64-linux" "aarch64-linux" "aarch64-darwin" ];
  };
}
