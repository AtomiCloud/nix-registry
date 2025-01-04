{ nixpkgs, trivialBuilders }:
with nixpkgs;
with (import ./pls/default.nix { inherit trivialBuilders nixpkgs; });
{
  inherit pls please;
}
