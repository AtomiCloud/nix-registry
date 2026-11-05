{ nixpkgs, go }:

# gorelease (golang.org/x/exp/cmd/gorelease): reports API-compatibility breaks
# between a module's working tree and a released base version, and suggests the
# next semver. It is in neither nixpkgs nor any tagged module, so it is pinned to
# a golang/exp commit here and built with the registry's own Go (`go` below is
# golang/go), which keeps the binary's stdlib on the same CVE-patched toolchain
# the registry guarantees. Consuming templates use it as a plain `gorelease`
# entry instead of building it themselves (dlint no-custom-derivations).
with nixpkgs;
(buildGoModule.override { inherit go; }) rec {
  pname = "gorelease";
  # golang/exp has no tags; the version records the pinned commit's date.
  version = "0-unstable-2026-10-14";
  rev = "f0251eb4aa3edefb2e1c158e57af104ae2de221a";

  meta = {
    owner = "golang";
    repo = "exp";
    mainProgram = "gorelease";
  };

  src = fetchurl {
    url = "https://github.com/${meta.owner}/${meta.repo}/archive/${rev}.tar.gz";
    sha256 = "sha256-FJ+YccpD1hNsf5Rm2dOR+1/5QggkwABEW1shTRztHqQ=";
  };

  vendorHash = "sha256-pPFxnYfw7EWIcxaW+tWLgvjcDvmKkH/0T7XX89ty6w0=";

  subPackages = [ "cmd/gorelease" ];

  # The upstream tests build fixture modules through a module proxy.
  doCheck = false;

  ldflags = [ "-w" "-s" ];
}
