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
  clickup_cli_raw = n."@krodak/clickup-cli";
  clickup_cli = trivialBuilders.writeShellScriptBin {
    name = "cup";
    version = clickup_cli_raw.version;
    text = ''
      export PATH="${nodejs}/bin:$PATH"
      ${clickup_cli_raw}/bin/cup "$@"
    '';
  };
  pagerduty_cli_raw = n."pagerduty-cli";
  pagerduty_cli = trivialBuilders.writeShellScriptBin {
    name = "pd";
    version = pagerduty_cli_raw.version;
    text = ''
      export PATH="${nodejs}/bin:$PATH"
      ${pagerduty_cli_raw}/bin/pd "$@"
    '';
  };

})
