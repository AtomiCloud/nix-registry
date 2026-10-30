#!/usr/bin/env bash
# shellcheck disable=SC2034

# The registry packages CI builds, smoke-tests and (on main) pushes to Attic.
#
# Sourced by nix-build.sh and cache-build.sh so both act on ONE list. They used
# to keep separate copies, and the cache list had fallen to 21 of the 43
# packages the build covered: everything it missed was built cold by every
# consumer.
#
# `tool-bundle-contract` is the bundle contract check: building it builds all
# nine tool aggregates and fails if any one's bin/ list drifted from what
# binWrapper/bundleContract.nix declares. It is listed here (rather than only in
# `nix flake check`) so the assertion runs on every platform this matrix covers
# — pkgs.docker is client-only off Linux, so the expected lists differ by system.
#
# That is also how `workspace-validator-runtime` is covered without a line of its
# own: the contract derivation takes the bundle as an input, so the bundle cannot
# fail to build without failing the contract. It is a PATH, not a binary, so
# there is no `--version` to run on it — what needs asserting is its contents,
# and that is exactly what the contract asserts.

REGISTRY_PACKAGES=(
  sg
  upstash
  action_docs
  typescript_json_schema
  swagger_typescript_api
  dotnetsay
  dotnet-ef
  mirrord
  pls
  toml-cli
  nix-share
  aws-export-credentials
  codemagic-cli-tools
  mmoney-cli
  cyanprint
  worktrunk
  atomiutils
  gardenio
  infrautils
  infralint
  tool-bundle-contract
  codecov
  dotnetPackage
  dotnet-pin-contract
  dotnetlint
  dn-inspect
  helmlint
  helm-schema
  attic
  cliproxyapi
  deadcode
  dlint
  coderabbit
  ccc
  md-mermaid-lint
  clickup_cli
  inspect
  pagerduty_cli
  dashboard-linter
  nsc
  obsidian_headless
  releaser
  resolver-smoke
)
