{ nixpkgs, gardenio, mirrord }:
let pkgs = nixpkgs; in

pkgs.runCommand "infrautils"
{
  buildInputs = [
    pkgs.kubernetes-helm
    pkgs.kubectl
    pkgs.k3d

    pkgs.docker
    pkgs.tilt

    gardenio
    mirrord
  ];
} ''
  mkdir -p $out/bin
  cp ${pkgs.kubernetes-helm}/bin/* $out/bin/
  cp ${pkgs.kubectl}/bin/* $out/bin/
  cp ${pkgs.k3d}/bin/* $out/bin/
  cp ${pkgs.docker}/bin/* $out/bin/
  cp ${pkgs.tilt}/bin/* $out/bin/
  cp ${gardenio}/bin/* $out/bin/
  cp ${mirrord}/bin/* $out/bin/
''
