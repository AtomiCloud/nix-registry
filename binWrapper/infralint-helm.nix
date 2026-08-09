{ nixpkgs, helmlint }:
# Axis-pure slice of `infralint`: the helm axis. Stripped wholesale by a node
# that declares no helm toolchain (`wo-helm`).
#
# helm itself stays out of `bin/` — helmlint carries its own helm on PATH via
# the override below, so this bundle contributes no kubernetes binaries of its
# own and remains composable next to `infrautils-k8s`.
let pkgs = nixpkgs; in
let hl = helmlint.override { helmPackage = pkgs.kubernetes-helm; }; in

pkgs.runCommand "infralint-helm"
{
  buildInputs = [
    pkgs.helm-docs
    hl
  ];
} ''
  mkdir -p $out/bin
  cp ${pkgs.helm-docs}/bin/* $out/bin/
  cp ${hl}/bin/* $out/bin/
''
