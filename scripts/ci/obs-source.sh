#!/usr/bin/env bash
set -eou pipefail

# Build obs's AGPL-3.0 Corresponding Source (its source plus every vendored Go module, see
# golang/obs/default.nix `source`) and copy the tarball into <dir> (default dist/), where
# atomi_release.yaml's @semantic-release/github `assets` picks it up. Run by the releaser's
# prepare step (@semantic-release/exec), so each registry release carries obs's source.

out="${1:-dist}"
mkdir -p "$out"
store="$(nix build .#obs-source --no-link --print-out-paths)"
cp "$store"/obs-*-source.tar.gz "$out"/
ls -l "$out"
