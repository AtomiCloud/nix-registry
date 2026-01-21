{ pkgs, pkgs-2511, pkgs-unstable, atomi }:
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
    nix-2511 = (
      with pkgs-2511;
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
nix-2511 //
nix-unstable //
atomipkgs
