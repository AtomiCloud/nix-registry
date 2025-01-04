#!/usr/bin/env bash
set -eou pipefail

# install dependencies
echo "⬇️ Installing Dependencies..."

echo "✅ Done!"

# run precommit
echo "🏃‍➡️ Running Pre-Commit..."
pre-commit run --all -v
echo "✅ Done!"
