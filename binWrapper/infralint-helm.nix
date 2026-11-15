{ nixpkgs, helmlint, chart-checks }:
# Axis-pure slice of `infralint`: the helm axis. Stripped wholesale by a node
# that declares no helm toolchain (`wo-helm`).
#
# helm itself stays out of `bin/` — helmlint and chart-checks carry their own
# helm on PATH, so this bundle contributes no kubernetes binaries of its own and
# remains composable next to `infrautils-k8s`.
let pkgs = nixpkgs; in
let hl = helmlint.override { helmPackage = pkgs.kubernetes-helm; }; in

pkgs.runCommand "infralint-helm"
{
  buildInputs = [
    pkgs.helm-docs
    hl
    chart-checks
  ];
} ''
  mkdir -p $out/bin
  ln -s ${pkgs.helm-docs}/bin/* $out/bin/
  ln -s ${hl}/bin/* $out/bin/
  ln -s ${chart-checks}/bin/* $out/bin/
''
