{ nixpkgs }:
with nixpkgs;
buildGoModule rec {
  pname = "dashboard-linter";
  version = "v0.2.0";

  meta = {
    owner = "grafana";
    repo = "dashboard-linter";
  };

  src = fetchurl {
    url = "https://github.com/${meta.owner}/${meta.repo}/archive/refs/tags/${version}.tar.gz";
    sha256 = "sha256-N0SA82Ve0Ck6/JUVRldJkgRcXi0w1BupnpLpk/AbHVU=";
  };

  vendorHash = "sha256-rWJqlsmGoCVhEbt0ersZP5RFaKcG0xq5jHy4Tm7SoU0=";

  doCheck = false;

  ldflags = [ "-w" "-s" "-a" ];
}
