{ nixpkgs }:
let pkgs = nixpkgs; in

pkgs.runCommand "atomiutils"
{
  buildInputs = [
    pkgs.coreutils
    pkgs.findutils
    pkgs.gnused
    pkgs.gnugrep
    pkgs.curl
    pkgs.jq
    pkgs.yq-go
    pkgs.bc
    pkgs.bash
  ];
} ''
  mkdir -p $out/bin
  cp ${pkgs.coreutils}/bin/* $out/bin/
  cp ${pkgs.findutils}/bin/* $out/bin/
  cp ${pkgs.gnused}/bin/* $out/bin/
  cp ${pkgs.gnugrep}/bin/* $out/bin/
  cp ${pkgs.curl}/bin/* $out/bin/
  cp ${pkgs.jq}/bin/* $out/bin/
  cp ${pkgs.yq}/bin/* $out/bin/
  cp ${pkgs.bc}/bin/* $out/bin/
  cp ${pkgs.bash}/bin/* $out/bin/
''
