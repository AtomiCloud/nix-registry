{ nixpkgs, trivialBuilders }:
with nixpkgs;
with (import ./pls/default.nix { inherit trivialBuilders nixpkgs; });
rec {
  inherit pls please;
  dotnetlint = import ./dotnetlint/default.nix { inherit trivialBuilders nixpkgs; };
}
