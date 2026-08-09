{ nixpkgs }:
# Axis-pure slice of `infrautils`: the docker axis. Stripped wholesale by a
# node that declares no docker toolchain (`wo-docker`).
let pkgs = nixpkgs; in

pkgs.runCommand "infrautils-docker"
{
  buildInputs = [
    pkgs.docker
  ];
} ''
  mkdir -p $out/bin
  cp ${pkgs.docker}/bin/* $out/bin/
''
