# A conforming template package list: plain declarative attributes only, so the
# cyanprint nix resolver can merge it when templates compose.
{ pkgs }:
{
  packages = with pkgs; [
    bash
    git
    jq
  ];
}
