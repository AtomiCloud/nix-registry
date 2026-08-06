{ nixpkgs, bun }:
with nixpkgs;
let
  version = "1.2.0";

  # Immutable source pin. The rev is authoritative and the tag is a comment, so the
  # rev was ASSERTED to resolve to that tag rather than taken on the comment:
  #   git ls-remote --tags .../releaser -> refs/tags/v1.2.0 = d41b68a0bff6...
  src = fetchFromGitHub {
    owner = "AtomiCloud";
    repo = "releaser";
    rev = "d41b68a0bff69e622ba334e187f2a55f37cd1efb"; # v1.2.0
    hash = "sha256-51de0dCCkPTG4Tc1IEsaeCBAnIOy74YByUG4NmNOjmE=";
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

  # The BYTES must report the version the derivation claims. This is not
  # belt-and-braces: `src` is a fixed-output derivation, so THE HASH IS THE
  # IDENTITY AND THE `rev` IS ONLY A HINT. If a bump changes `rev` but leaves a
  # previous `hash` in place, and that content is already in the store, nix
  # reuses the OLD SOURCE, never fetches the rev at all, and produces a
  # derivation NAMED for the new version out of the old bytes -- silently, and
  # with exit 0.
  #
  # That is not hypothetical. It was demonstrated on this package while writing
  # this: v1.0.0's hash paired with v1.2.0's rev built a `releaser-1.2.0` whose
  # binary reported 1.0.0 and lacked `conventions --check`. This phase is what
  # makes that mismatch fail instead of ship.
  # The whole output is captured and compared exactly, rather than grepped. A
  # `grep -Fx` here would pass on any output that CONTAINS a matching line, so a
  # binary printing the right version plus a warning line would satisfy it. For a
  # guard whose only job is to catch a mismatch, "contains" is the wrong relation.
  doInstallCheck = true;
  installCheckPhase = ''
    runHook preInstallCheck
    actualVersion="$("$out/bin/releaser" --version)"
    test "$actualVersion" = "${version}"
    runHook postInstallCheck
  '';

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
