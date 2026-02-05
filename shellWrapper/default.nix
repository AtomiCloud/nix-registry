{ nixpkgs, trivialBuilders }:
with nixpkgs;
with (import ./pls/default.nix { inherit trivialBuilders nixpkgs; });
rec {
  inherit pls please;
  deadcode = import ./deadcode/default.nix { inherit trivialBuilders nixpkgs; };
  dotnetlint = import ./dotnetlint/default.nix { inherit trivialBuilders nixpkgs; };
  dn-inspect = import ./dn-inspect/default.nix { inherit trivialBuilders nixpkgs; };
  helmlint = import ./helmlint/default.nix { inherit trivialBuilders nixpkgs; };
}
