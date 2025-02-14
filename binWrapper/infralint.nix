{ nixpkgs }:
let pkgs = nixpkgs; in

pkgs.runCommand "infralint"
{
  buildInputs = [
    pkgs.hadolint
    pkgs.helm-docs

    pkgs.terraform-docs
    pkgs.tfsec
    pkgs.tflint
  ];
} ''
  mkdir -p $out/bin
  cp ${pkgs.hadolint}/bin/* $out/bin/
  cp ${pkgs.helm-docs}/bin/* $out/bin/

  cp ${pkgs.terraform-docs}/bin/* $out/bin/
  cp ${pkgs.tfsec}/bin/* $out/bin/
  cp ${pkgs.tflint}/bin/* $out/bin/
''
