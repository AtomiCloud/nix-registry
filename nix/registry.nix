{ pkgs, pkgs-2411, atomi }:
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
      with pkgs;
      { }
    );
    nix-2411 = (
      with pkgs-2411;
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
nix-2411 //
nix-unstable //
atomipkgs
