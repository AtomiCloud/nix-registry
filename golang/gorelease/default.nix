{ nixpkgs, go }:
with nixpkgs;
(buildGoModule.override { inherit go; }) rec {
  pname = "gorelease";
  # golang/exp has no tags; pinned commit
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

  # upstream tests need a module proxy
  doCheck = false;

  ldflags = [ "-w" "-s" ];
}
