{ nixpkgs, go }:

# problemgen: definition-first RFC 9457 problems. One problems.yaml per service module is
# linted, diffed for breaking changes, and turned into typed Go / TypeScript / C# / Dart code
# plus the Problem record (error-portal row) and frontend catalog entries.
#
# The source lives in-tree (src/), like chart-checks' script, so the registry release IS the
# tool release. A static Go binary, wrapped so `diff --git` finds git (its one runtime input).
# The licence and notice files of every linked Go module ship in share/licenses/problemgen.
let
  version = "0.1.0";

  problemgen = (nixpkgs.buildGoModule.override { inherit go; }) {
    pname = "problemgen";
    inherit version;
    src = ./src;
    vendorHash = "sha256-MYHY7N+LqNnfNuIdUqtb4Cr24UQqi+4tbc1ysdzJ2BY=";
    env.CGO_ENABLED = "0";
    ldflags = [ "-w" "-s" ];
    nativeBuildInputs = [ nixpkgs.makeWrapper ];
    # go test: every planted bad definition and every diff pair is refused with its code;
    # the diff --git test drives a real repository.
    doCheck = true;
    nativeCheckInputs = [ nixpkgs.git ];
    postInstall = ''
      wrapProgram "$out/bin/problemgen" --prefix PATH : ${nixpkgs.lib.makeBinPath [ nixpkgs.git ]}
      # Third-party notices: every LICENSE/NOTICE/COPYING/PATENTS file of the vendored modules,
      # kept under its module path (Apache-2.0 requires NOTICE files to travel with binaries).
      (cd vendor && find . -type f \( -iname 'LICENSE*' -o -iname 'NOTICE*' -o -iname 'COPYING*' -o -iname 'PATENTS*' \) \
        -exec install -Dm444 {} "$out/share/licenses/problemgen/{}" \;)
      install -Dm444 vendor/modules.txt "$out/share/licenses/problemgen/modules.txt"
    '';
    meta.mainProgram = "problemgen";
  };

  # End-to-end run of the built binary in the sandbox (no network): lint refusals, diff
  # refusals, generate -> verify -> drift detection, record shape.
  tests = nixpkgs.runCommand "problemgen-tests"
    { nativeBuildInputs = [ problemgen nixpkgs.bash nixpkgs.gnugrep nixpkgs.gnused nixpkgs.coreutils nixpkgs.git ]; }
    ''
      bash ${./tests}/run.sh --problemgen ${problemgen}/bin/problemgen --src ${./src} --out ${problemgen}
      mkdir -p "$out"
    '';
in
problemgen // { passthru = (problemgen.passthru or { }) // { inherit tests; }; }
