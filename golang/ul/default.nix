{ nixpkgs, go }:

# ul: the ubiquitous-language tool. It loads every UL.yaml of a repo, overlays them into one final
# UL, and serves it as JSON (`ul/v1`), Markdown, lookups, `ul check` rules and generated UL.md files.
# Spec: design/registry-tools/ul.md in the diene corpus.
#
# `ul check` also reads the doc pages under docs/ (.md with a CommonMark parser, .dh.md through the
# `dh` tool, which must be on PATH for a repo that has .dh.md pages) and holds them to the word
# lists; with --translations fixed (the default) it holds translations to each word's fixed term.
#
# The source lives in-tree (src/), like problemgen, so the registry release IS the tool
# release. A static Go binary, wrapped so the repo scan finds git (its one runtime input).
# The licence and notice files of every linked Go module ship in share/licenses/ul.
#
# Name clash: util-linux also ships a `ul` (an old underline filter). Inside a nix dev shell
# the registry's `ul` comes first on PATH; outside one, call it by full path.
let
  version = "0.2.0";

  ul = (nixpkgs.buildGoModule.override { inherit go; }) {
    pname = "ul";
    inherit version;
    src = ./src;
    vendorHash = "sha256-+kCsPMLO8jE7Gq7iQmPiBnfpC9I6DuWzpaF/EO4ez/w=";
    env.CGO_ENABLED = "0";
    ldflags = [ "-w" "-s" "-X main.version=${version}" ];
    nativeBuildInputs = [ nixpkgs.makeWrapper ];
    # go test: every planted-failure fixture is refused with its one code, the clean fixture
    # passes, goldens, overlay and lookup order, and the 1,000-word speed test. The git-scan
    # tests drive a real repository.
    doCheck = true;
    nativeCheckInputs = [ nixpkgs.git ];
    postInstall = ''
      wrapProgram "$out/bin/ul" --prefix PATH : ${nixpkgs.lib.makeBinPath [ nixpkgs.git ]}
      # Third-party notices: every LICENSE/NOTICE/COPYING/PATENTS file of the vendored modules,
      # kept under its module path (Apache-2.0 requires NOTICE files to travel with binaries).
      (cd vendor && find . -type f \( -iname 'LICENSE*' -o -iname 'NOTICE*' -o -iname 'COPYING*' -o -iname 'PATENTS*' \) \
        -exec install -Dm444 {} "$out/share/licenses/ul/{}" \;)
      install -Dm444 vendor/modules.txt "$out/share/licenses/ul/modules.txt"
    '';
    meta.mainProgram = "ul";
  };

  # End-to-end run of the built binary in the sandbox (no network): the clean fixture is
  # green, every planted failure is red with its code, drift
  # detection, and the speed bound.
  tests = nixpkgs.runCommand "ul-tests"
    {
      nativeBuildInputs = [
        ul
        nixpkgs.bash
        nixpkgs.gnugrep
        nixpkgs.gnused
        nixpkgs.gawk
        nixpkgs.coreutils
        nixpkgs.jq
        nixpkgs.git
      ];
    }
    ''
      bash ${./tests}/run.sh --ul ${ul}/bin/ul --src ${./src} --out ${ul}
      mkdir -p "$out"
    '';
in
ul // { passthru = (ul.passthru or { }) // { inherit tests; }; }
