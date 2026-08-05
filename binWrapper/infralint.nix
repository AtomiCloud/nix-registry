{ nixpkgs, helmlint }:
let pkgs = nixpkgs; in
let hl = helmlint.override { helmPackage = pkgs.kubernetes-helm; }; in

pkgs.runCommand "infralint"
{
  buildInputs = [
    pkgs.hadolint
    pkgs.helm-docs

    pkgs.terraform-docs
    pkgs.tfsec
    pkgs.tflint
    hl

    pkgs.kubeconform
    pkgs.kyverno
  ];
} ''
  mkdir -p $out/bin
  cp ${pkgs.hadolint}/bin/* $out/bin/
  cp ${pkgs.helm-docs}/bin/* $out/bin/

  cp ${pkgs.terraform-docs}/bin/* $out/bin/
  cp ${pkgs.tfsec}/bin/* $out/bin/
  cp ${pkgs.tflint}/bin/* $out/bin/
  cp ${hl}/bin/* $out/bin/

  cp ${pkgs.kubeconform}/bin/* $out/bin/
  cp ${pkgs.kyverno}/bin/* $out/bin/
''
