{ nixpkgs, go }:
(nixpkgs.buildGoModule.override { inherit go; }) {
  pname = "kubectl-validate";
  # main @ e230257 (2026-10-14): embeds core schemas for Kubernetes 1.23 to 1.35 (v0.0.4 stops at 1.30).
  version = "0.0.4-unstable-2026-10-14";

  src = nixpkgs.fetchFromGitHub {
    owner = "kubernetes-sigs";
    repo = "kubectl-validate";
    rev = "e230257a35cf48938dfcd74a195014cdeb764376";
    hash = "sha256-wjaPjU+zsk/BjaeDmHdIFsQ9Aw8PJM27JVh6duemhV0=";
  };

  vendorHash = null;

  subPackages = [ "." ];

  doCheck = false;

  ldflags = [ "-w" "-s" ];

  meta.mainProgram = "kubectl-validate";
}
