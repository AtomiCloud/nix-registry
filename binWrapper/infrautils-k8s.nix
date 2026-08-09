{ nixpkgs }:
# Axis-pure slice of `infrautils`: the kubernetes/helm axis. Stripped wholesale
# by a node that declares no helm toolchain (`wo-helm`).
let pkgs = nixpkgs; in

pkgs.runCommand "infrautils-k8s"
{
  buildInputs = [
    pkgs.kubernetes-helm
    pkgs.kubectl
    pkgs.k3d
    pkgs.kubectx
  ];
} ''
  mkdir -p $out/bin
  cp ${pkgs.kubernetes-helm}/bin/* $out/bin/
  cp ${pkgs.kubectl}/bin/* $out/bin/
  cp ${pkgs.k3d}/bin/* $out/bin/
  cp ${pkgs.kubectx}/bin/* $out/bin/
''
