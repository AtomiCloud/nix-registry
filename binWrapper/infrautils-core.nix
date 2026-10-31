{ nixpkgs, gardenio, mirrord }:
# Axis-pure slice of `infrautils`: the tools that survive every strip axis.
# Nothing here shells out to a docker daemon or to helm/kubectl, so a node that
# strips either axis still composes this bundle.
let pkgs = nixpkgs; in

pkgs.runCommand "infrautils-core"
{
  buildInputs = [
    pkgs.tilt

    pkgs.opentofu

    gardenio
    mirrord
  ];
} ''
  mkdir -p $out/bin
  ln -s ${pkgs.tilt}/bin/* $out/bin/

  ln -s ${pkgs.opentofu}/bin/* $out/bin/

  ln -s ${gardenio}/bin/* $out/bin/
  ln -s ${mirrord}/bin/* $out/bin/
''
