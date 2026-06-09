#!/usr/bin/env bash
# Regenerates the vendored npm lockfiles and hashes consumed by buildNpmPackage.
#
# For every package declared in node/<ver>/node-packages.json this:
#   1. resolves the npm registry tarball and records its hash (tarballHash),
#   2. strips devDependencies and generates a prod-only package-lock.json,
#      vendored at node/<ver>/pkgs/<attr>/package-lock.json,
#   3. records the npm-deps hash (npmDepsHash) via prefetch-npm-deps.
#
# Usage: scripts/local/gen-node.sh <node-version>   (e.g. 22)
set -euo pipefail

NODE_VER="${1:?usage: gen-node.sh <node-version>}"
ROOT="node/${NODE_VER}"
MANIFEST="${ROOT}/node-packages.json"

[ -f "$MANIFEST" ] || {
  echo "no manifest at $MANIFEST" >&2
  exit 1
}

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

# Work on a copy of the manifest, then move it into place at the end.
manifest_out="$tmp/manifest.json"
cp "$MANIFEST" "$manifest_out"

for attr in $(jq -r 'keys[]' "$MANIFEST"); do
  npm="$(jq -r --arg a "$attr" '.[$a].npm' "$MANIFEST")"
  ver="$(jq -r --arg a "$attr" '.[$a].version' "$MANIFEST")"
  base="${npm##*/}"
  url="https://registry.npmjs.org/${npm}/-/${base}-${ver}.tgz"
  echo ">> ${attr} (${npm}@${ver})"

  # 1. Tarball hash (SRI) for fetchurl.
  tarball_hash="$(nix store prefetch-file --json "$url" | jq -r '.hash')"

  # 2. Prod-only lockfile.
  work="$tmp/$attr"
  mkdir -p "$work"
  curl -fsSL "$url" -o "$work/pkg.tgz"
  tar xzf "$work/pkg.tgz" -C "$work"
  (
    cd "$work/package"
    jq 'del(.devDependencies)' package.json >package.json.tmp
    mv package.json.tmp package.json
    # --ignore-scripts: don't run the package's prepare/build during resolution.
    npm install --package-lock-only --legacy-peer-deps --ignore-scripts >/dev/null 2>&1
    [ -f package-lock.json ] || {
      echo "failed to generate lockfile for $attr" >&2
      exit 1
    }
  )
  mkdir -p "${ROOT}/pkgs/${attr}"
  cp "$work/package/package-lock.json" "${ROOT}/pkgs/${attr}/package-lock.json"

  # 3. npm-deps hash.
  deps_hash="$(prefetch-npm-deps "$work/package/package-lock.json" 2>/dev/null | tail -1)"

  jq --arg a "$attr" --arg t "$tarball_hash" --arg d "$deps_hash" \
    '.[$a].tarballHash = $t | .[$a].npmDepsHash = $d' \
    "$manifest_out" >"$manifest_out.tmp"
  mv "$manifest_out.tmp" "$manifest_out"
done

cp "$manifest_out" "$MANIFEST"
echo "Updated $MANIFEST and ${ROOT}/pkgs/*/package-lock.json"
