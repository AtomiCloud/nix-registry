{ nixpkgs, bun }:
with nixpkgs;
let
  version = "1.3.2";

  # Immutable source pin. The rev is authoritative and the tag is a comment, so the
  # rev was ASSERTED to resolve to that tag rather than taken on the comment:
  #   git ls-remote --tags .../releaser -> refs/tags/v1.3.2 = 72b9109d8776...
  src = fetchFromGitHub {
    owner = "AtomiCloud";
    repo = "releaser";
    rev = "72b9109d87764062bb4ffb7546422cffca807530"; # v1.3.2
    hash = "sha256-GkiKyBZUfW5R8eBV4OQ48bvrqYxfaBcHgY6xdiN3gjU=";
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
  # The refusal NAMES ITS REASON. `test` fails silently, which left the build log
  # showing only "Running phase: installCheckPhase" and nothing else -- a guard
  # that refuses correctly but mutely is hard to tell apart from a guard that
  # broke, and the diagnosis it withholds is exactly the non-obvious one.
  doInstallCheck = true;
  installCheckPhase = ''
    runHook preInstallCheck
    actualVersion="$("$out/bin/releaser" --version)"
    if [ "$actualVersion" != "${version}" ]; then
      echo "ERROR: releaser version mismatch." >&2
      echo "  derivation claims: ${version}" >&2
      echo "  built binary reports: $actualVersion" >&2
      echo "The 'src' hash is almost certainly stale. In a fixed-output" >&2
      echo "derivation the hash IS the identity and the rev is only a hint, so" >&2
      echo "a new rev paired with an old hash reuses the OLD bytes without" >&2
      echo "ever fetching. Set 'hash = lib.fakeHash' and re-derive it from" >&2
      echo "what nix reports; do not hand-edit it." >&2
      exit 1
    fi
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
      Built from a pinned source tag with Bun's single-file compiler. The tag is
      deliberately not named here: a version written into prose goes stale on
      every bump, and this sentence had already been wrong for several of them.
      The authoritative version is the `version` binding above, which the
      installCheck asserts against the built bytes.
    '';
    mainProgram = "releaser";
    homepage = "https://github.com/AtomiCloud/releaser";
    license = licenses.mit;
    platforms = [ "x86_64-linux" "aarch64-linux" "aarch64-darwin" ];
  };
}
