#!/usr/bin/env bash

cache="$1"

set -eou pipefail

if [ -z "$cache" ]; then
  echo "Usage: $0 <cache>"
  exit 1
fi

BUILD_TARGETS=""

echo "🔨 Building $BUILD_TARGETS"
# shellcheck disable=SC2086
TO_PUSH=$(nix build \
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
  .#cyanprint \
  .#atomiutils \
  .#gardenio \
  .#infrautils \
  .#infralint \
  .#codecov \
  .#dotnetlint \
  .#helmlint \
  --print-out-paths)
echo "✅ Successfully built all devShells"

echo "🫸 Pushing all shells to Attic $cache"
# shellcheck disable=SC2086
attic push "$cache" $TO_PUSH
echo "✅ Successfully pushed all shells to Attic $cache"

echo "🎉 All devShells have been built and pushed to cache!"
