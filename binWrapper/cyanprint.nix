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
