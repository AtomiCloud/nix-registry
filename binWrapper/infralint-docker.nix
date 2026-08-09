{ nixpkgs }:
# Axis-pure slice of `infralint`: the docker axis. Stripped wholesale by a node
# that declares no docker toolchain (`wo-docker`).
#
# skopeo lives here rather than in `infralint-core`: it is a container-image
# tool, and a node that has opted out of the docker axis has no images to
# inspect or copy. It does not need a running daemon, so if a `wo-docker` node
# ever does want registry inspection it should compose this bundle explicitly.
let pkgs = nixpkgs; in

pkgs.runCommand "infralint-docker"
{
  buildInputs = [
    pkgs.hadolint
    pkgs.skopeo
  ];
} ''
  mkdir -p $out/bin
  cp ${pkgs.hadolint}/bin/* $out/bin/
  cp ${pkgs.skopeo}/bin/* $out/bin/
''
