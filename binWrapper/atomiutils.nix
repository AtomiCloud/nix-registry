{ nixpkgs }:
let pkgs = nixpkgs; in

pkgs.runCommand "atomiutils"
{
  nativeBuildInputs = [ pkgs.rsync ];
  buildInputs = [

    pkgs.toybox
    pkgs.coreutils
    pkgs.findutils
    pkgs.uutils-coreutils-noprefix


    pkgs.gnused
    pkgs.gnugrep
    pkgs.gnutar
    pkgs.gawk
    pkgs.wget

    pkgs.curl
    pkgs.gomplate

    pkgs.jq
    pkgs.yq-go


    pkgs.bc
    pkgs.bash
  ];
} ''
  mkdir -p $out/bin
  cp ${pkgs.toybox}/bin/* $out/bin/
  cp -f ${pkgs.coreutils}/bin/* $out/bin/
  cp -f ${pkgs.findutils}/bin/* $out/bin/
  cp -f ${pkgs.uutils-coreutils-noprefix}/bin/* $out/bin/

  cp -f ${pkgs.gnused}/bin/* $out/bin/
  cp -f ${pkgs.gnugrep}/bin/* $out/bin/
  cp -f ${pkgs.gnutar}/bin/* $out/bin/
  cp -f ${pkgs.gawk}/bin/* $out/bin/

  cp -f ${pkgs.curl}/bin/* $out/bin/
  cp -f ${pkgs.gomplate}/bin/* $out/bin/

  cp ${pkgs.jq}/bin/* $out/bin/
  cp ${pkgs.yq-go}/bin/* $out/bin/

  cp -f ${pkgs.bc}/bin/* $out/bin/
  cp ${pkgs.bash}/bin/* $out/bin/
''
