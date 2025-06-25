{ pkgs, pkgs-2505, pkgs-unstable, atomi }:
let

  all = {
    atomipkgs = (
      with atomi;
      {
        inherit
          sg
          pls;
      }
    );
    nix-unstable = (
      with pkgs-unstable;
      { }
    );
    nix-2505 = (
      with pkgs-2505;
      {
        node2nix = nodePackages.node2nix;
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
  };
in
with all;
nix-2505 //
nix-unstable //
atomipkgs
