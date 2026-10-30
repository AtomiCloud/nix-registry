#!/usr/bin/env bash

cache="$1"

set -eou pipefail

if [ -z "$cache" ]; then
  echo "Usage: $0 <cache>"
  exit 1
fi

# The same list nix-build.sh builds and smoke-tests; see packages.sh.
# shellcheck disable=SC1091 # packages.sh is read at run time, not followed by the hook
source "$(dirname "$0")/packages.sh"

# Run after nix-build.sh in the same job, so every path is already in the store
# and this build only resolves them.
echo "🔨 Resolving ${#REGISTRY_PACKAGES[@]} registry packages"
TO_PUSH=$(nix build --no-link --print-out-paths "${REGISTRY_PACKAGES[@]/#/.#}")
echo "✅ Resolved all registry packages"

# `attic push` uploads each path's whole runtime closure, so what a package
# pulls in from this registry (gardenio and mirrord under infrautils, say) is
# pushed with it.
echo "🫸 Pushing registry packages to Attic $cache"
# shellcheck disable=SC2086
attic push "$cache" $TO_PUSH
echo "✅ Successfully pushed registry packages to Attic $cache"
