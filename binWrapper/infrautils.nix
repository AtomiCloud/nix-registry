{ nixpkgs, gardenio, mirrord }:
let pkgs = nixpkgs; in

pkgs.runCommand "infrautils"
{
  buildInputs = [
    pkgs.kubernetes-helm
    pkgs.kubectl
    pkgs.k3d
    pkgs.kubectx

    pkgs.docker
    pkgs.tilt

    pkgs.opentofu

    gardenio
    mirrord
  ];
} ''
  mkdir -p $out/bin
  ln -s ${pkgs.kubernetes-helm}/bin/* $out/bin/
  ln -s ${pkgs.kubectl}/bin/* $out/bin/
  ln -s ${pkgs.k3d}/bin/* $out/bin/
  ln -s ${pkgs.kubectx}/bin/* $out/bin/

  ln -s ${pkgs.docker}/bin/* $out/bin/
  ln -s ${pkgs.tilt}/bin/* $out/bin/

  ln -s ${pkgs.opentofu}/bin/* $out/bin/

  ln -s ${gardenio}/bin/* $out/bin/
  ln -s ${mirrord}/bin/* $out/bin/
''
