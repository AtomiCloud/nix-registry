{ trivialBuilders, nixpkgs }:

trivialBuilders.writeShellScriptBin {
  name = "dn-inspect";
  version = "0.1.0";
  text = ''
    export PATH="${nixpkgs.lib.makeBinPath [ nixpkgs.dotnet-sdk_8 nixpkgs.jq nixpkgs.coreutils nixpkgs.findutils ]}:$PATH"
    ${./dn-inspect.sh} "$@"
  '';
}
