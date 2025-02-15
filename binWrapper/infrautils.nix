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
    pkgs.skopeo

    pkgs.opentofu

    gardenio
    mirrord
  ];
} ''
  mkdir -p $out/bin
  cp ${pkgs.kubernetes-helm}/bin/* $out/bin/
  cp ${pkgs.kubectl}/bin/* $out/bin/
  cp ${pkgs.k3d}/bin/* $out/bin/
  cp ${pkgs.kubectx}/bin/* $out/bin/

  cp ${pkgs.docker}/bin/* $out/bin/
  cp ${pkgs.tilt}/bin/* $out/bin/

  cp ${pkgs.opentofu}/bin/* $out/bin/

  cp ${gardenio}/bin/* $out/bin/
  cp ${mirrord}/bin/* $out/bin/
''
