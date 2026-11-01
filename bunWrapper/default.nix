{ nixpkgs, bun }:
let trivialBuilders = import ./trivialBuilders.nix { inherit nixpkgs bun; }; in
let buildBunCli = import ./buildBunCli.nix { inherit nixpkgs; defaultBun = bun; }; in
{
  md-mermaid-lint = import ./md-mermaid-lint { inherit nixpkgs bun trivialBuilders; };
  releaser = import ./releaser { inherit nixpkgs bun; };
  resolver-smoke = import ./resolver-smoke { inherit nixpkgs bun trivialBuilders; };
  skills-sync = import ./skills-sync { inherit nixpkgs bun trivialBuilders; };
  bun-cli-fixture = import ./bun-cli-fixture { inherit buildBunCli; };
}
