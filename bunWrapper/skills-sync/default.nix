{ nixpkgs, bun, trivialBuilders }:

let
  # package.json is the single source of the version: the CLI reads it at
  # runtime, so a version written twice could disagree with itself.
  version = (builtins.fromJSON (builtins.readFile ./package.json)).version;
in
trivialBuilders.writeBunScriptBin {
  inherit version;
  name = "skills-sync";
  src = ./.;

  # git is the tool's own dependency: it tells a tracked vendored tree from an
  # untracked one, and a tree that is right on disk and absent from git is not
  # fresh. It is declared here rather than assumed on the consumer's PATH.
  runtimeInputs = with nixpkgs; [ git ];
}
