#!/usr/bin/env bash
set -eou pipefail

# Build every registry package and smoke-test the resulting binaries.
#
# This lives in a script rather than inline in ci.yaml because the release gate
# and the informational macOS leg are two SEPARATE jobs (see the comments in
# .github/workflows/ci.yaml). Inlining would mean two copies of the package
# list, and a registry whose whole job is gaining packages would drift them
# apart within days — the macOS leg would quietly stop covering new packages.
#
# The per-arch caches start cold on first run, so the heavy rust workspaces
# (worktrunk, attic) are compiled from source. --max-jobs 1 serialises
# derivations so only one of those links at a time, keeping peak memory within
# the 16GB runner. Once the Namespace /nix cache is warm this run is ~1 min
# regardless.

# `.#tool-bundle-contract` is the bundle contract check: building it builds all
# eight tool aggregates and fails if any one's bin/ list drifted from what
# binWrapper/bundleContract.nix declares. It is listed here (rather than only in
# `nix flake check`) so the assertion runs on every platform this matrix covers
# — pkgs.docker is client-only off Linux, so the expected lists differ by system.

echo "🔨 Building and smoke-testing all registry packages..."

nix shell --max-jobs 1 --cores 2 nixpkgs#bash \
  .#sg \
  .#upstash \
  .#action_docs \
  .#typescript_json_schema \
  .#swagger_typescript_api \
  .#dotnetsay \
  .#dotnet-ef \
  .#mirrord \
  .#pls \
  .#toml-cli \
  .#nix-share \
  .#aws-export-credentials \
  .#codemagic-cli-tools \
  .#mmoney-cli \
  .#cyanprint \
  .#worktrunk \
  .#atomiutils \
  .#gardenio \
  .#infrautils \
  .#infralint \
  .#tool-bundle-contract \
  .#codecov \
  .#dotnetlint \
  .#dn-inspect \
  .#helmlint \
  .#attic \
  .#cliproxyapi \
  .#deadcode \
  .#dlint \
  .#coderabbit \
  .#ccc \
  .#md-mermaid-lint \
  .#clickup_cli \
  .#inspect \
  .#pagerduty_cli \
  .#dashboard-linter \
  .#nsc \
  .#obsidian_headless \
  .#releaser \
  .#resolver-smoke \
  -c bash -c '
  sg --version &&
  upstash --version &&
  action-docs --version &&
  typescript-json-schema --version &&
  swagger-typescript-api --version &&
  dotnetsay &&
  dotnet-ef --version &&
  mirrord --version &&
  pls --version &&
  toml --version &&
  nix-share &&
  cyanprint --version &&
  wt --version &&
  garden version &&
  codecov --version &&
  dotnetlint --version &&
  dn-inspect --version &&
  deadcode --version &&
  dlint --version &&
  coderabbit --version &&
  ccc --version &&
  md-mermaid-lint --version &&
  cup --version &&
  pd --version &&
  helmlint --version &&
  inspect --help &&
  attic --version &&
  cli-proxy-api --help &&
  dashboard-linter --help &&
  nsc version &&
  app-store-connect --version &&
  mmoney --version &&
  ob --version &&
  releaser --version &&
  resolver-smoke --version &&
  RESOLVER_SMOKE=resolver-smoke ./bunWrapper/resolver-smoke/tests/run.sh &&
  echo "🎉 Done"'
