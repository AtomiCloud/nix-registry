{
  description = "Atomi Nix Registry";
  inputs = {
    # util
    flake-utils.url = "github:numtide/flake-utils";
    treefmt-nix.url = "github:numtide/treefmt-nix";
    pre-commit-hooks.url = "github:cachix/pre-commit-hooks.nix";
    fenix.url = "github:nix-community/fenix";
    # Pinned to f587c48 (release 2.20.0), the last good commit. The tip 7aeed0e
    # (2.21.0) fails to build: cyanprint/Cargo.toml declares `bollard = "*"`, and
    # the semantic-release commit regenerated Cargo.lock floating bollard to
    # 0.21.0, which renamed MountTypeEnum -> MountType, while
    # cyanprint/src/coord.rs still imports the old name. Unpin once upstream fixes
    # the bollard import.
    cyanprintpkgs.url = "github:AtomiCloud/sulfone.iridium/f587c48";
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
    # Intel macOS (x86_64-darwin) was dropped in v3.0.0; the registry now targets
    # Linux (x86_64, aarch64) and Apple Silicon (aarch64-darwin) only.
    (flake-utils.lib.eachSystem [ "x86_64-linux" "aarch64-linux" "aarch64-darwin" ]
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
