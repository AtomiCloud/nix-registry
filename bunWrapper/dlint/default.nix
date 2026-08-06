{ nixpkgs, bun, trivialBuilders }:

trivialBuilders.writeBunScriptBin {
  name = "dlint";
  version = "0.1.0";
  src = ./.;
}
