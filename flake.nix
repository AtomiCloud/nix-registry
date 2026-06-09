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
    , nixpkgs-unstable
    } @inputs:
    (flake-utils.lib.eachDefaultSystem
      (
        system:
        let
          allowUnfreePredicate =
            pkg:
            builtins.elem (nixpkgs-2605.lib.getName pkg) [ "inspect" ];
          pkgs-unstable = nixpkgs-unstable.legacyPackages.${system};
          # inspect (built from the primary pkgs, now 26.05) is unfree, so the
          # predicate is applied here rather than to the node-only 25.11 set.
          pkgs-2605 = import nixpkgs-2605 {
            inherit system;
            config.allowUnfreePredicate = allowUnfreePredicate;
          };
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
              inherit pkgs pkgs-2605 pkgs-unstable;
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
