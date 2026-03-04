{ nixpkgs, bun, trivialBuilders }:

let
  version = "0.1.0";
in
trivialBuilders.writeBunScriptBin {
  inherit version;
  name = "md-mermaid-lint";
  src = ./.;

  # Add any runtime dependencies if needed
  buildInputs = with nixpkgs; [
    # Add dependencies here if needed
  ];
}
