{ trivialBuilders, nixpkgs }:

let
  version = "0.1.0";
in
trivialBuilders.writeShellApplication {
  name = "go-validator";
  inherit version;
  runtimeShell = nixpkgs.runtimeShell;
  runtimeInputs = with nixpkgs; [
    bash
    coreutils
    git
    go
    go-tools
    gotools
    golangci-lint
    jq
  ];
  text = ''
    GO_VALIDATOR_VERSION="${version}"
    ${builtins.readFile ./go-validator.sh}
  '';
}
