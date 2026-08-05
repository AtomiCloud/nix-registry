#!/usr/bin/env bash
# Fixture regeneration command: republish the vendored tree from its source.
# dlint owns "regenerate, then refuse if the tree moved"; this script is the
# consuming repository's half of that contract.
set -euo pipefail
vendor="third_party/skills"
rm -rf "${vendor}"
mkdir -p "${vendor}"
touch "${vendor}/.gitkeep"
cp -R source/skills/. "${vendor}/"
