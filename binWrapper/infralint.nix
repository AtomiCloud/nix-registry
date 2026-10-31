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
    pkgs.skopeo
  ];
} ''
  mkdir -p $out/bin
  ln -s ${pkgs.hadolint}/bin/* $out/bin/
  ln -s ${pkgs.helm-docs}/bin/* $out/bin/

  ln -s ${pkgs.terraform-docs}/bin/* $out/bin/
  ln -s ${pkgs.tfsec}/bin/* $out/bin/
  ln -s ${pkgs.tflint}/bin/* $out/bin/
  ln -s ${hl}/bin/* $out/bin/

  ln -s ${pkgs.kubeconform}/bin/* $out/bin/
  ln -s ${pkgs.kyverno}/bin/* $out/bin/
  ln -s ${pkgs.skopeo}/bin/* $out/bin/
''
