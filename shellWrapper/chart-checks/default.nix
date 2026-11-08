{ trivialBuilders, nixpkgs }:

let
  # Version of the chart-checks tool
  version = "0.1.0";

  # Every binary the script calls. Nothing is assumed to be on the consumer's PATH.
  #
  # Step 0 of the goal picked `kubectl-validate` over `kubeconform` + a CRD -> JSON-schema
  # converter: it validates core kinds (schemas embedded for Kubernetes 1.23 to 1.30), the
  # installed CRDs (`--local-crds`) and unknown fields, with no network. See README.md.
  runtimeInputs = with nixpkgs; [
    bash
    coreutils
    curl
    findutils
    git
    gnugrep
    gnutar
    gzip
    jq
    kubectl-validate
    kubernetes-helm
    kyverno
    yq-go
  ];

  chart-checks = trivialBuilders.writeShellApplication {
    name = "chart-checks";
    inherit version runtimeInputs;
    runtimeShell = nixpkgs.runtimeShell;
    text = ''
      set -euo pipefail
      CHART_CHECKS_VERSION="${version}"
      ${builtins.readFile ./chart-checks.sh}
    '';
  };

  # The tests run in a nix build, so they have NO network: that is the proof that `check`
  # (and a stamp-matching `install`) never reach out. Sources are file:// URLs, local git
  # repositories and, on Linux, a loopback OCI registry.
  tests = nixpkgs.runCommand "chart-checks-tests"
    {
      nativeBuildInputs = runtimeInputs ++ [ chart-checks ]
        ++ nixpkgs.lib.optionals nixpkgs.stdenv.isLinux [ nixpkgs.distribution nixpkgs.netcat ];
      CC_TEST_OCI = if nixpkgs.stdenv.isLinux then "1" else "";
    }
    ''
      export HOME="$TMPDIR/home"
      mkdir -p "$HOME"
      bash ${./tests}/run.sh --chart-checks ${chart-checks}/bin/chart-checks
      mkdir -p "$out"
    '';
in
chart-checks // { passthru = { inherit tests; }; }
