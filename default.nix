{ nixpkgs, nixpkgs-2605, nixpkgs-dotnet, nixpkgs-unstable, fenix }:
let trivialBuilders = import ./trivial.nix { inherit nixpkgs; }; in
let
  # Node.js 22 CLI packages, built the official nixpkgs way via buildNpmPackage.
  node22 = import ./node/22/export.nix { nixpkgs = nixpkgs; nodejs = nixpkgs.nodejs_22; };
  # .NET SDK pin, exported under its own name so the .NET wrappers below and any
  # consuming node name the same SDK instead of each re-stating the version.
  dotnetSdk = import ./dotnet/default.nix { inherit nixpkgs nixpkgs-dotnet; };

  # Shell
  shell = (import ./shellWrapper/default.nix {
    inherit nixpkgs trivialBuilders;
    inherit (dotnetSdk) dotnetPackage;
  });

  # Python
  python = {
    aws-export-credentials = import ./python/aws-export-credentials/default.nix { inherit nixpkgs; };
    codemagic-cli-tools = import ./python/codemagic-cli-tools/default.nix { inherit nixpkgs; };
    mmoney-cli = import ./python/mmoney-cli/default.nix { inherit nixpkgs; };
  };

  # Go
  golang = {
    # The registry guarantees the Go version, so a template can keep a plain entry
    # instead of carrying an overrideAttrs (R14 / D7 RESOLVER-SHAPE).
    go = import ./golang/go/default.nix { inherit nixpkgs; };
    nix-share = import ./golang/nix-share/default.nix { inherit nixpkgs; };
    ccc = import ./golang/ccc/default.nix { inherit nixpkgs; };
    dashboard-linter = import ./golang/dashboard-linter/default.nix { inherit nixpkgs; };
  };

  # dotnet
  dotnet = import ./nuget/default.nix { inherit nixpkgs; };

  # bin wrapper
  bin = rec {
    mirrord = import ./binWrapper/mirrord.nix { inherit nixpkgs; };
    cyanprint = import ./binWrapper/cyanprint.nix { inherit nixpkgs; };
    atomiutils = import ./binWrapper/atomiutils.nix { inherit nixpkgs; };
    infrautils = import ./binWrapper/infrautils.nix { inherit nixpkgs gardenio mirrord; };
    infralint = import ./binWrapper/infralint.nix { inherit nixpkgs; helmlint = shell.helmlint; };

    # Axis-pure slices of the two aggregates above, so a node can declare a
    # toolchain without the tools it strips. The full bundles stay untouched for
    # everyone else; see binWrapper/bundleContract.nix for what each provides.
    infrautils-core = import ./binWrapper/infrautils-core.nix { inherit nixpkgs gardenio mirrord; };
    infrautils-docker = import ./binWrapper/infrautils-docker.nix { inherit nixpkgs; };
    infrautils-k8s = import ./binWrapper/infrautils-k8s.nix { inherit nixpkgs; };
    infralint-core = import ./binWrapper/infralint-core.nix { inherit nixpkgs; };
    infralint-docker = import ./binWrapper/infralint-docker.nix { inherit nixpkgs; };
    infralint-helm = import ./binWrapper/infralint-helm.nix { inherit nixpkgs; helmlint = shell.helmlint; };

    # The closed PATH a fleet node's validator hooks run under. Hoisted out of
    # nix/pre-commit.nix on the nodes that all carried the same buildEnv.
    workspace-validator-runtime = import ./binWrapper/workspace-validator-runtime.nix {
      inherit nixpkgs atomiutils;
    };

    tool-bundle-contract = import ./binWrapper/bundleContract.nix {
      inherit nixpkgs;
      bundles = {
        inherit
          infrautils
          infralint
          infrautils-core
          infrautils-docker
          infrautils-k8s
          infralint-core
          infralint-docker
          infralint-helm;
      };
      unions = {
        workspace-validator-runtime = {
          bundle = workspace-validator-runtime;
          parts = [ atomiutils nixpkgs.git ];
          # What a node's scripts/validate/*.sh hooks actually invoke: the five
          # tools their buildEnvs named explicitly (bash, git, jq, rg, yq) plus
          # the coreutils/findutils/grep/sed staples every one of those scripts
          # uses.
          requires = [
            "awk"
            "bash"
            "cat"
            "cp"
            "find"
            "git"
            "grep"
            "jq"
            "rg"
            "sed"
            "sort"
            "yq"
          ];
        };
      };
    };
    gardenio = import ./binWrapper/gardenio.nix { inherit nixpkgs; };
    codecov = import ./binWrapper/codecov.nix { inherit nixpkgs; };
    coderabbit = import ./binWrapper/coderabbit.nix { inherit nixpkgs; };
    cliproxyapi = import ./binWrapper/cliproxyapi.nix { inherit nixpkgs; };
    inspect = import ./binWrapper/inspect.nix { inherit nixpkgs; };
    nsc = import ./binWrapper/nsc.nix { inherit nixpkgs; };
  };

  rust = import ./rust/default.nix { inherit nixpkgs fenix; };

  # bun wrapper
  bun = import ./bunWrapper { inherit nixpkgs; bun = nixpkgs-unstable.bun; };

in

shell
// python
// golang
// node22
// bin
// rust
// dotnet
// dotnetSdk
  // bun
