# Node.js 22 CLI packages, built the official nixpkgs way via buildNpmPackage.
#
# Package metadata (npm name, version, tarball + npm-deps hashes) lives in
# ./node-packages.json; the build logic lives in ./build-npm.nix. To add or
# upgrade a package, edit node-packages.json and run `pls gen:node:22`.
{ nixpkgs, nodejs }:
let
  manifest = builtins.fromJSON (builtins.readFile ./node-packages.json);
  buildNpm = import ./build-npm.nix { inherit nixpkgs nodejs; };
in
builtins.mapAttrs (attr: spec: buildNpm (spec // { inherit attr; })) manifest
