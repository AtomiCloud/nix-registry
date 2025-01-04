{ nixpkgs, fenix }:
let rust = (import ./lib.nix { inherit nixpkgs fenix; }).rust; in
{
  toml-cli = import ./toml-cli/default.nix { inherit nixpkgs rust; };
}
