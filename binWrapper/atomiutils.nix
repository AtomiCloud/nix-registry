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
    pkgs.ripgrep
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
  ln -s ${pkgs.toybox}/bin/* $out/bin/
  ln -sf ${pkgs.coreutils}/bin/* $out/bin/
  ln -sf ${pkgs.findutils}/bin/* $out/bin/
  ln -sf ${pkgs.uutils-coreutils-noprefix}/bin/* $out/bin/

  ln -sf ${pkgs.gnused}/bin/* $out/bin/
  ln -sf ${pkgs.gnugrep}/bin/* $out/bin/
  ln -sf ${pkgs.ripgrep}/bin/* $out/bin/
  ln -sf ${pkgs.gnutar}/bin/* $out/bin/
  ln -sf ${pkgs.gawk}/bin/* $out/bin/

  ln -sf ${pkgs.curl}/bin/* $out/bin/
  ln -sf ${pkgs.gomplate}/bin/* $out/bin/

  ln -s ${pkgs.jq}/bin/* $out/bin/
  ln -s ${pkgs.yq-go}/bin/* $out/bin/

  ln -sf ${pkgs.bc}/bin/* $out/bin/
  ln -s ${pkgs.bash}/bin/* $out/bin/
''
