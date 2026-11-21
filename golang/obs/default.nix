{ nixpkgs, go }:

# obs: the observability check tool. `obs query --static` parses every alert and dashboard query
# with the embedded VictoriaMetrics (MetricsQL) and VictoriaLogs (LogsQL) parsers; `obs query
# --live` runs them on a seed in the embedded engines. No network, no containers.
#
# The source lives in-tree (src/), like problemgen, so the registry release IS the tool release.
# One static Go binary (`go` = Go 1.27.4 from golang/obs/go.nix, see there). The licence and
# notice files of every linked module ship in share/licenses/obs. AGPL-3.0-only (golang/obs/LICENSE).
let
  version = "0.5.0";

  obs = (nixpkgs.buildGoModule.override { inherit go; }) {
    pname = "obs";
    inherit version;
    src = ./src;
    vendorHash = "sha256-eiFYiYt6rVvd+6xT1WnM+NnMADsz5uEpDp0Gc4dHLXo=";
    env.CGO_ENABLED = "0";
    env.OBS_REQUIRE_LLVM = "1";
    nativeCheckInputs = [ nixpkgs.llvmPackages.llvm nixpkgs.git ]; # git: the tests of the git-ignored folders
    ldflags = [ "-w" "-s" "-X main.version=${version}" ];
    # third_party/VictoriaLogs is its own Go module (wired by a go.mod replace), not part of this one.
    excludedPackages = [ "third_party" ];
    # go test: field walker, tree drift against the VictoriaLogs tag, rules, seed shape, and
    # `obs` end to end on temp projects.
    doCheck = true;
    # The Go tests read the fixture contract and the golden files under tests/ (outside src/): give them
    # the tests folder next to the source root, where ../../../tests resolves from internal/<pkg>.
    preCheck = ''
      cp -r --no-preserve=mode ${./tests} ../tests
      # TestNoRealLandscapeAnywhere also scans golang/obs/README.md (beside the tests folder).
      cp --no-preserve=mode ${./README.md} ../README.md
    '';
    postInstall = ''
      # Third-party notices: every LICENSE/NOTICE/COPYING/PATENTS file of the vendored modules,
      # kept under its module path (Apache-2.0 requires NOTICE files to travel with binaries),
      # and the AGPL-3.0 text of obs itself.
      (cd vendor && find . -type f \( -iname 'LICENSE*' -o -iname 'NOTICE*' -o -iname 'COPYING*' -o -iname 'PATENTS*' \) \
        -exec install -Dm444 {} "$out/share/licenses/obs/{}" \;)
      install -Dm444 vendor/modules.txt "$out/share/licenses/obs/modules.txt"
      install -Dm444 ${./LICENSE} "$out/share/licenses/obs/LICENSE"
      install -Dm444 ${./src/internal/alerts/gfexpr/LICENSE-grafana-AGPL} "$out/share/licenses/obs/grafana/LICENSE"
    '';
    meta = {
      mainProgram = "obs";
      description = "Observability checks with no running stack: MetricsQL and LogsQL parsed and run on a seed";
      license = nixpkgs.lib.licenses.agpl3Only;
    };
  };

  # End-to-end run of the built binary in the sandbox (no network): the good fixtures run green, the generated Go and
  # TypeScript, C# and Dart compile (go, tsc, the dotnet SDK and dart are inputs here),
  # every planted failure runs red naming file + rule + token, the vendored tree equals its tag.
  tests = nixpkgs.runCommand "obs-tests"
    { nativeBuildInputs = [ obs nixpkgs.bash nixpkgs.gnugrep nixpkgs.gnused nixpkgs.coreutils nixpkgs.findutils nixpkgs.diffutils go nixpkgs.typescript nixpkgs.dotnet-sdk_10 nixpkgs.dart nixpkgs.llvmPackages.llvm nixpkgs.git ]; }
    ''
      OBS_REQUIRE_LLVM=1 bash ${./tests}/run.sh --obs ${obs}/bin/obs --src ${./src} --out ${obs}
      mkdir -p "$out"
    '';

  # The AGPL-3.0 Corresponding Source of the obs binary: this tool's source plus every vendored
  # module (the Go proxy supplies them at build time, so the repository alone is not the whole
  # source). The release attaches it (atomi_release.yaml, @semantic-release/exec + github assets).
  # Deterministic: sorted, zero mtime and owner, gzip without a name or time.
  source = nixpkgs.runCommand "obs-source-${version}"
    { nativeBuildInputs = [ nixpkgs.gnutar nixpkgs.gzip ]; }
    ''
      name="obs-${version}-source"
      mkdir -p "build/$name" "$out"
      cp -r --no-preserve=mode ${./src}/. "build/$name/"
      cp -r --no-preserve=mode ${obs.goModules} "build/$name/vendor"
      # the fixtures and golden files the Go tests and tests/run.sh read (the tests find tests/ in an ancestor)
      cp -r --no-preserve=mode ${./tests} "build/$name/tests"
      install -Dm444 ${./LICENSE} "build/$name/LICENSE"
      install -Dm444 ${./README.md} "build/$name/README.md"
      tar --sort=name --mtime=@0 --owner=0 --group=0 --numeric-owner -C build -cf - "$name" \
        | gzip -n -9 > "$out/$name.tar.gz"
    '';
in
obs // { passthru = (obs.passthru or { }) // { inherit tests source; }; }
