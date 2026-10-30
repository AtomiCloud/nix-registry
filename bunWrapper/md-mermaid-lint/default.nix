{ nixpkgs, bun, trivialBuilders }:

let
  version = "0.1.0";
in
trivialBuilders.writeBunScriptBin {
  inherit version;
  name = "md-mermaid-lint";
  src = ./.;

  # Fixed-output hash of the production node_modules; see writeBunScriptBin.
  depsHash = "sha256-CMbhQF4Ae8zWH3XMnL2gfo1XlQKaDRp1V5vwhaAHPWA=";

  # Add any runtime dependencies if needed
  buildInputs = with nixpkgs; [
    # Add dependencies here if needed
  ];
}
