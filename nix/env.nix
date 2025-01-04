{ pkgs, packages }:
with packages;
{
  system = [
    coreutils
    findutils
    gnugrep
    gnused
    bash
    jq
    yq
  ];

  dev = [
    pls
    git
  ];

  main = [
    infisical
    node2nix
    nix-prefetch
    bundix
  ];

  lint = [
    # core
    treefmt
    gitlint
    shellcheck
    sg
  ];

  releaser = [
    sg
  ];
}
