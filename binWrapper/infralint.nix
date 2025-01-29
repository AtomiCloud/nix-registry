{ nixpkgs }:
let pkgs = nixpkgs; in

pkgs.runCommand "infralint"
{
  buildInputs = [
    pkgs.hadolint
    pkgs.helm-docs
  ];
} ''
  mkdir -p $out/bin
  cp ${pkgs.hadolint}/bin/* $out/bin/
  cp ${pkgs.helm-docs}/bin/* $out/bin/
''
