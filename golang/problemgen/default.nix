{ nixpkgs, go }:

# problemgen: definition-first RFC 9457 problems. One problems.yaml per service module is
# linted, diffed for breaking changes, and turned into typed Go / TypeScript / C# / Dart code
# plus the Problem record (error-portal row) and frontend catalog entries.
#
# The source lives in-tree (src/), like chart-checks' script, so the registry release IS the
# tool release. A single static Go binary: no runtime inputs.
let
  version = "0.1.0";

  problemgen = (nixpkgs.buildGoModule.override { inherit go; }) {
    pname = "problemgen";
    inherit version;
    src = ./src;
    vendorHash = "sha256-MYHY7N+LqNnfNuIdUqtb4Cr24UQqi+4tbc1ysdzJ2BY=";
    env.CGO_ENABLED = "0";
    ldflags = [ "-w" "-s" ];
    # go test: every planted bad definition and every diff pair is refused with its code.
    doCheck = true;
    meta.mainProgram = "problemgen";
  };

  # End-to-end run of the built binary in the sandbox (no network): lint refusals, diff
  # refusals, generate -> verify -> drift detection, record shape.
  tests = nixpkgs.runCommand "problemgen-tests"
    { nativeBuildInputs = [ problemgen nixpkgs.bash nixpkgs.gnugrep nixpkgs.gnused nixpkgs.coreutils ]; }
    ''
      bash ${./tests}/run.sh --problemgen ${problemgen}/bin/problemgen --src ${./src}
      mkdir -p "$out"
    '';
in
problemgen // { passthru = (problemgen.passthru or { }) // { inherit tests; }; }
