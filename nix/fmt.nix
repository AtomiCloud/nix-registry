{ treefmt-nix, pkgs, ... }:
let
  fmt = {
    projectRootFile = "flake.nix";

    # enable or disable formatters, see https://github.com/numtide/treefmt-nix#supported-programs
    programs = {
      nixpkgs-fmt.enable = true;
      prettier.enable = true;
      # nodePackages was removed from nixpkgs as of 26.05; prettier now lives at the top level.
      prettier.package = pkgs.prettier;
      shfmt.enable = true;
      actionlint.enable = true;
    };


  };
in
(treefmt-nix.lib.evalModule pkgs fmt).config.build.wrapper


