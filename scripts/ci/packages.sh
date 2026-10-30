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
# The contract checks the axis bundles and `workspace-validator-runtime`; none
# of them is a binary, so none has a `--version` for nix-build.sh to run, and
# what needs asserting is their contents, which the contract asserts. They are
# still listed below on lines of their own because the contract's OUTPUT is a
# manifest that references none of them: `attic push` uploads an output's
# runtime closure, so without their own entries consumers could not substitute
# them from the cache. Listing them adds no build — the contract already built
# them.

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
  infrautils-core
  infrautils-docker
  infrautils-k8s
  infralint-core
  infralint-docker
  infralint-helm
  workspace-validator-runtime
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
