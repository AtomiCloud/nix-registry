{ trivialBuilders, nixpkgs, nodejs }:
let
  n = import ./composition.nix { pkgs = nixpkgs; inherit nodejs; };
in
with n;
(rec {
  sg_raw = n."@kirinnee/semantic-generator".override {
    buildInputs = [
      nixpkgs.nodePackages.npm
    ];
    nativeBuildInputs = [ nixpkgs.pkg-config ];
  };
  sg = trivialBuilders.writeShellScriptBin {
    name = "sg";
    version = sg_raw.version;
    text = ''
      export PATH="${nodejs}/bin:$PATH"
      ${sg_raw}/bin/sg "$@"
    '';
  };
  upstash = n."@upstash/cli";
  action_docs = n."action-docs";
  typescript_json_schema = n."typescript-json-schema";
  swagger_typescript_api = n."swagger-typescript-api";
  openapi_to_postmanv2 = n."openapi-to-postmanv2";
  happy_coder_raw = n."happy-coder";
  happy_coder = trivialBuilders.writeShellScriptBin {
    name = "happy";
    version = happy_coder_raw.version;
    text = ''
      export PATH="${nodejs}/bin:$PATH"
      ${happy_coder_raw}/lib/node_modules/happy-coder/bin/happy.mjs "$@"
    '';
  };

})
