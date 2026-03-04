{ nixpkgs, bun }:
let trivialBuilders = import ./trivialBuilders.nix { inherit nixpkgs bun; }; in
{
  md-mermaid-lint = import ./md-mermaid-lint { inherit nixpkgs bun trivialBuilders; };
}
