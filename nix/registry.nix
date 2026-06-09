{ pkgs, pkgs-2605, pkgs-unstable, atomi }:
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
          # Node.js + prefetch-npm-deps regenerate the vendored npm lockfiles
          # and npm-deps hashes consumed by buildNpmPackage (see `pls gen:node:22`).
          nodejs_22
          prefetch-npm-deps
          ;
      }
    );
  };
in
with all;
nix-2605 //
nix-unstable //
atomipkgs
