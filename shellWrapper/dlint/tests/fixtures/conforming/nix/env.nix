# A conforming template environment list.
{ pkgs }:
{
  env = with pkgs; [
    coreutils
    findutils
  ];
}
