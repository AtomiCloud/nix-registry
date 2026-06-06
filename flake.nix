{
  description = "Atomi Nix Registry";
  inputs = {
    # util
    flake-utils.url = "github:numtide/flake-utils";
    treefmt-nix.url = "github:numtide/treefmt-nix";
    pre-commit-hooks.url = "github:cachix/pre-commit-hooks.nix";
    fenix.url = "github:nix-community/fenix";
    cyanprintpkgs.url = "github:AtomiCloud/sulfone.iridium";
    worktrunkpkgs.url = "github:max-sixty/worktrunk";
    atticpkgs.url = "github:zhaofengli/attic";

    # registry
    nixpkgs-unstable.url = "github:nixos/nixpkgs/nixos-unstable";
    nixpkgs-2605.url = "github:nixos/nixpkgs/nixos-26.05";
    # node2nix and the nodePackages set were removed from nixpkgs as of 26.05,
    # so the node/22 ecosystem is sourced from the last release that still has them.
    nixpkgs-2511.url = "github:nixos/nixpkgs/nixos-25.11";
  };
  outputs =
    { self

      # utils
    , flake-utils
    , treefmt-nix
    , pre-commit-hooks
    , cyanprintpkgs
    , worktrunkpkgs
    , fenix
    , atticpkgs
      # registries
    , nixpkgs-2605
    , nixpkgs-2511
    , nixpkgs-unstable
    } @inputs:
    (flake-utils.lib.eachDefaultSystem
      (
        system:
        let
          pkgs-unstable = nixpkgs-unstable.legacyPackages.${system};
          pkgs-2605 = nixpkgs-2605.legacyPackages.${system};
          pkgs-2511 = nixpkgs-2511.legacyPackages.${system};
          fenixpkgs = fenix.packages.${system};
          cyanprint = cyanprintpkgs.packages.${system};
          worktrunk = worktrunkpkgs.packages.${system};
          attic = atticpkgs.packages.${system};
          pre-commit-lib = pre-commit-hooks.lib.${system};
        in
        let pkgs = pkgs-2605; in
        with rec {
          pre-commit = import ./nix/pre-commit.nix {
            inherit pre-commit-lib formatter;
            packages = registry;
          };
          formatter = import ./nix/fmt.nix {
            inherit treefmt-nix pkgs;
          };
          registry = import ./nix/registry.nix
            {
              inherit pkgs pkgs-2605 pkgs-2511 pkgs-unstable;
              atomi = packages;
            };
          env = import ./nix/env.nix {
            inherit pkgs;
            packages = registry;
          };
          devShells = import ./nix/shells.nix {
            inherit pkgs env;
            packages = registry;
            shellHook = checks.pre-commit-check.shellHook;
          };
          checks = {
            pre-commit-check = pre-commit;
            format = formatter;
          };
          packages = import ./default.nix
            {
              fenix = fenixpkgs;
              nixpkgs = pkgs;
              nixpkgs-2605 = pkgs-2605;
              nixpkgs-2511 = pkgs-2511;
              nixpkgs-unstable = pkgs-unstable;
            } // {
            cyanprint = cyanprint.default;
            worktrunk = worktrunk.default;
            attic = attic.default;
          };
          defaultPackage = pkgs.symlinkJoin {
            name = "all";
            paths = builtins.attrValues packages;
          };
        };
        {
          inherit checks formatter packages devShells defaultPackage;
        }
      )
    )
  ;

}
