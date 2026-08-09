{ nixpkgs }:
with nixpkgs;
let
  inherit (stdenv.hostPlatform) system;
  throwSystem = throw "Unsupported system: ${system}";

  # No x86_64-darwin entry, deliberately. sulfone.lite does publish a
  # cyanprint_<version>_darwin_amd64.tar.gz, and the dotnet-base node's hand-written
  # derivation selected it — but this flake dropped Intel macOS in v3.0.0 and only
  # instantiates x86_64-linux, aarch64-linux and aarch64-darwin, so a fourth key
  # here would be a platform this registry can never build and a digest nothing
  # would ever re-verify. See the eachSystem list in flake.nix, which is where that
  # decision is recorded; the hoist narrows that one platform on purpose.
  plat = {
    x86_64-linux = "linux_amd64";
    aarch64-linux = "linux_arm64";

    aarch64-darwin = "darwin_arm64";
  }.${system} or throwSystem;

  sha256 = {
    x86_64-linux = "sha256-PeFuwTrym0IPxFtW5UUFsB4FnrhRZhRzYbOjHd4y2GA=";
    aarch64-linux = "sha256-l8ZbXBi6x+fSJFFTP2e7S8v7/uqN5WcX5v/pidrvIC8=";

    aarch64-darwin = "sha256-nqvipKksCM0HkmG8u1z5VktW86kIt+3c0o1Y8DOdBC0=";
  }.${system} or throwSystem;
in
let version = "4.9.2"; in

stdenv.mkDerivation (finalAttrs: {
  pname = "cyanprint";
  inherit version;

  # The release archive unpacks flat (no top-level directory), unlike
  # gardenio's, so stdenv can't infer a sourceRoot to cd into.
  sourceRoot = ".";

  # Nothing here is compiled: the only inputs are an unpacker and, on Linux, the
  # libraries autoPatchelfHook writes into the prebuilt binary's RPATH. Splitting
  # host from build deps keeps that distinction honest rather than incidental.
  strictDeps = true;

  # The upstream Linux binary is dynamically linked and its ELF interpreter
  # points at a Nix-store glibc that is not otherwise in this package's closure,
  # so it fails with "cannot execute: required file not found" on any clean
  # machine (e.g. CI runners). autoPatchelfHook rewrites the interpreter/RPATH to
  # the glibc + libgcc we depend on here, pulling them into the runtime closure.
  # Both are Linux-only (glibc/autoPatchelfHook don't exist on Darwin, whose
  # Mach-O binary needs no patching), so guard them behind isLinux.
  nativeBuildInputs = lib.optionals stdenv.hostPlatform.isLinux [ autoPatchelfHook ];
  buildInputs = lib.optionals stdenv.hostPlatform.isLinux [ stdenv.cc.cc.lib glibc ];

  # cyanprint ships as a Bun single-file executable: the whole application is
  # APPENDED to the Bun runtime, past the end of the ELF's sections, and located at
  # startup from a trailer. stdenv's default fixup runs `strip`, which rewrites the
  # file and drops that trailing payload — the binary stays executable and keeps its
  # size to within a few KiB, so nothing fails loudly; it simply degrades into a bare
  # Bun runtime. `cyanprint --version` then prints Bun's version and `cyanprint probe`
  # dies with `error: Script not found "probe"`.
  # This bit every release packaged since autoPatchelfHook was introduced, 4.7.0
  # included. Keep the binary unstripped; there are no symbols worth reclaiming in a
  # prebuilt artifact anyway.
  dontStrip = true;

  installPhase = ''
    runHook preInstall
    install -Dm755 cyanprint "$out/bin/cyanprint"
    runHook postInstall
  '';

  # The `dontStrip` note above describes a degradation that is SILENT: a stripped
  # cyanprint still runs, still reports a version, and only fails once a template
  # is actually rendered. A comment cannot catch a regression; running the binary
  # can. `--version` printing this exact version is the cheapest assertion that the
  # Bun payload is still attached, because a bare Bun runtime prints Bun's version
  # instead. Skipped when the build machine cannot execute the host binary, since a
  # cross build has nothing to run it with.
  doInstallCheck = stdenv.buildPlatform.canExecute stdenv.hostPlatform;
  installCheckPhase = ''
    runHook preInstallCheck
    "$out/bin/cyanprint" --version | grep -Fx "cyanprint ${version}"
    runHook postInstallCheck
  '';

  # `builtins.fetchurl` downloads at EVALUATION time: the bytes never become a
  # fixed-output derivation, so they cannot be substituted from the binary cache and
  # every evaluator needs network access of its own. `fetchurl` is the fetcher the
  # rest of this registry uses (see mirrord.nix) and takes the same digest, since
  # both hash the file itself.
  src = fetchurl {
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
