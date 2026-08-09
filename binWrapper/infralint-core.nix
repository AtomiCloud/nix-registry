{ nixpkgs }:
# Axis-pure slice of `infralint`: the linters that survive every strip axis.
# Terraform/OpenTofu and plain-manifest kubernetes linters need neither a docker
# daemon nor helm.
let pkgs = nixpkgs; in

pkgs.runCommand "infralint-core"
{
  buildInputs = [
    pkgs.terraform-docs
    pkgs.tfsec
    pkgs.tflint

    pkgs.kubeconform
    pkgs.kyverno
  ];
} ''
  mkdir -p $out/bin
  cp ${pkgs.terraform-docs}/bin/* $out/bin/
  cp ${pkgs.tfsec}/bin/* $out/bin/
  cp ${pkgs.tflint}/bin/* $out/bin/

  cp ${pkgs.kubeconform}/bin/* $out/bin/
  cp ${pkgs.kyverno}/bin/* $out/bin/
''
