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
    nodejs_22
    prefetch-npm-deps
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
