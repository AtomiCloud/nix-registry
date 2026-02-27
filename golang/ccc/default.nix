{ nixpkgs }:
with nixpkgs;
buildGoModule rec {
  pname = "ccc";
  version = "v1.6.2";

  meta = {
    owner = "kidandcat";
    repo = "ccc";
  };

  src = fetchurl {
    url = "https://github.com/${meta.owner}/${meta.repo}/archive/refs/tags/${version}.tar.gz";
    sha256 = "sha256-dol/S1xfMuLSb3hjIQIk85PaN6lgWuYVMUl3zfJUpIg=";
  };

  vendorHash = "sha256-7sDFP9a7SQr148jt7bGCAx6m5R3HHPT3+RxbpUYjrPY=";

  doCheck = false;

  ldflags = [ "-w" "-s" ];
}
