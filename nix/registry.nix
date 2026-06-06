{ pkgs, pkgs-2605, pkgs-2511, pkgs-unstable, atomi }:
let

  all = {
    atomipkgs = (
      with atomi;
      {
        inherit
          sg
          pls
          md-mermaid-lint;
      }
    );
    nix-unstable = (
      with pkgs-unstable;
      { }
    );
    nix-2605 = (
      with pkgs-2605;
      {
        yq = yq-go;
        inherit
          coreutils
          findutils
          gnugrep
          gnused
          jq
          bash
          git
          infisical
          treefmt
          gitlint
          shellcheck
          nix-prefetch
          bundix
          ;
      }
    );
    # node2nix and the nodePackages set were removed from nixpkgs as of 26.05,
    # so it is sourced from the last release (25.11) that still has them.
    nix-2511 = (
      with pkgs-2511;
      {
        node2nix = nodePackages.node2nix;
      }
    );
  };
in
with all;
nix-2605 //
nix-2511 //
nix-unstable //
atomipkgs
