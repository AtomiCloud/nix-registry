{
  description = "Atomi Nix Registry";
  inputs = {
    # util
    flake-utils.url = "github:numtide/flake-utils";
    treefmt-nix.url = "github:numtide/treefmt-nix";
    pre-commit-hooks.url = "github:cachix/pre-commit-hooks.nix";
    fenix.url = "github:nix-community/fenix";
    worktrunkpkgs.url = "github:max-sixty/worktrunk";
    atticpkgs.url = "github:zhaofengli/attic";

    # registry
    nixpkgs-unstable.url = "github:nixos/nixpkgs/nixos-unstable";
    nixpkgs-2605.url = "github:nixos/nixpkgs/nixos-26.05";

    # The .NET SDK ONLY, pinned to an EXACT COMMIT rather than to the channel.
    #
    # A .NET repository's global.json names an exact SDK patch with
    # `rollForward: disable`, so "close enough" is a hard build failure, not a
    # drift warning. The registry's own nixpkgs-2605 pin moves on the registry's
    # cadence and the fleet's .NET nodes move theirs on theirs; whenever those
    # two disagree by a patch, a node that took the registry's dotnetlint would
    # get an SDK its global.json refuses.
    #
    # So this input is the FLEET's pin, not the registry's: it is the exact
    # revision the .NET nodes pin their own nixpkgs-2605 to. It is deliberately
    # narrow — only dotnet/default.nix reads it — so agreeing with the fleet on
    # the SDK does not drag every other package in this registry onto a
    # different channel revision.
    #
    # nixos-26.05, dotnet-sdk_10 = 10.0.302
    nixpkgs-dotnet.url = "github:NixOS/nixpkgs/445d861c6d31b4af0c79d8d4be2331f762a361d7";
  };
  outputs =
    { self

      # utils
    , flake-utils
    , treefmt-nix
    , pre-commit-hooks
    , worktrunkpkgs
    , fenix
    , atticpkgs
      # registries
    , nixpkgs-2605
    , nixpkgs-dotnet
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
          # The SDK-only pin; see the nixpkgs-dotnet input comment.
          pkgs-dotnet = nixpkgs-dotnet.legacyPackages.${system};
          # inspect (built from the primary pkgs, now 26.05) is unfree, so the
          # predicate is applied here rather than to the node-only 25.11 set.
          pkgs-2605 = import nixpkgs-2605 {
            inherit system;
            config.allowUnfreePredicate = allowUnfreePredicate;
          };
          fenixpkgs = fenix.packages.${system};
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
            # Asserts every tool aggregate's bin/ list against its declared
            # contract, so bundle contents can never drift silently.
            tool-bundle-contract = packages.tool-bundle-contract;
            # Asserts the .NET SDK the wrappers ship really is the fleet-pinned
            # patch, by running it. See dotnet/default.nix.
            dotnet-pin-contract = packages.dotnet-pin-contract;
          };
          packages = import ./default.nix
            {
              fenix = fenixpkgs;
              nixpkgs = pkgs;
              nixpkgs-2605 = pkgs-2605;
              nixpkgs-dotnet = pkgs-dotnet;
              nixpkgs-unstable = pkgs-unstable;
            } // {
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
