{ nixpkgs, bun }:
let trivialBuilders = import ./trivialBuilders.nix { inherit nixpkgs bun; }; in
{
  dlint = import ./dlint { inherit nixpkgs bun trivialBuilders; };
  md-mermaid-lint = import ./md-mermaid-lint { inherit nixpkgs bun trivialBuilders; };
  releaser = import ./releaser { inherit nixpkgs bun; };
  skills-sync = import ./skills-sync { inherit nixpkgs bun trivialBuilders; };
}
