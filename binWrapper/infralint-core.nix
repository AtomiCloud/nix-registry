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
  ln -s ${pkgs.terraform-docs}/bin/* $out/bin/
  ln -s ${pkgs.tfsec}/bin/* $out/bin/
  ln -s ${pkgs.tflint}/bin/* $out/bin/

  ln -s ${pkgs.kubeconform}/bin/* $out/bin/
  ln -s ${pkgs.kyverno}/bin/* $out/bin/
''
