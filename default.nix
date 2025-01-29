{ nixpkgs, nixpkgs-2411, fenix }:
let trivialBuilders = import ./trivial.nix { inherit nixpkgs; }; in
let
  node22 = import ./node/22/export.nix { inherit trivialBuilders; nixpkgs = nixpkgs-2411; nodejs = nixpkgs-2411.nodejs_22; };
  # Shell
  shell = (import ./shellWrapper/default.nix { inherit nixpkgs trivialBuilders; });

  # Python
  python = {
    aws-export-credentials = import ./python/aws-export-credentials/default.nix { inherit nixpkgs; };
  };

  # Go
  golang = {
    nix-share = import ./golang/nix-share/default.nix { inherit nixpkgs; };
  };

  # dotnet
  dotnet = import ./nuget/default.nix { inherit nixpkgs; };

  # bin wrapper
  bin = {
    mirrord = import ./binWrapper/mirrord.nix { inherit nixpkgs; };
    atomiutils = import ./binWrapper/atomiutils.nix { inherit nixpkgs; };
  };

  rust = import ./rust/default.nix { inherit nixpkgs fenix; };

in

shell
// python
// golang
// node22
// bin
// rust
  // dotnet
