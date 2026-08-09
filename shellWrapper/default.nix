{ nixpkgs, trivialBuilders, dotnetPackage }:
with nixpkgs;
with (import ./pls/default.nix { inherit trivialBuilders nixpkgs; });
rec {
  inherit pls please;
  deadcode = import ./deadcode/default.nix { inherit trivialBuilders nixpkgs; };
  dlint = import ./dlint/default.nix { inherit trivialBuilders nixpkgs; };
  go-validator = import ./go-validator/default.nix { inherit trivialBuilders nixpkgs; };
  # Both .NET wrappers take the registry's SDK pin (dotnet/default.nix) as their
  # default `dotnetPackage`, so the pin is one decision in one place.
  dotnetlint = import ./dotnetlint/default.nix { inherit trivialBuilders nixpkgs dotnetPackage; };
  dn-inspect = import ./dn-inspect/default.nix { inherit trivialBuilders nixpkgs dotnetPackage; };
  helmlint = import ./helmlint/default.nix { inherit trivialBuilders nixpkgs; };
  # No trivialBuilders: this one builds on nixpkgs' writeShellApplication, see its comment.
  helm-schema = import ./helm-schema/default.nix { inherit nixpkgs; };
}
