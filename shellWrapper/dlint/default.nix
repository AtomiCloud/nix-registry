{ trivialBuilders, nixpkgs }:

let
  # Version of the dlint tool
  version = "0.1.0";
in
# `writeShellApplication` (not `writeShellScriptBin`) because its checkPhase runs
  # shellcheck as well as shellDryRun, and `dlint.sh` is inlined rather than
  # referenced so that the shellcheck actually covers it. `runtimeInputs` puts every
  # binary the checks call into the runtime closure; nothing is assumed to be on the
  # consumer's PATH. Individual packages are declared rather than the `atomiutils`
  # bundle: a bundle plus its own members collide inside a `buildEnv`.
trivialBuilders.writeShellApplication {
  name = "dlint";
  inherit version;
  runtimeShell = nixpkgs.runtimeShell;
  runtimeInputs = with nixpkgs; [
    bash
    coreutils
    findutils
    gawk
    git
    gnugrep
    gnused
    jq
    ripgrep
    yq-go
  ];
  text = ''
    DLINT_VERSION="${version}"
    ${builtins.readFile ./dlint.sh}
  '';
}
