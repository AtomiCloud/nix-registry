#!/usr/bin/env bash
set -euo pipefail

echo "Recursively searching for dotnet projects..."

# Find all .csproj, .fsproj files recursively
find . -type f \( -name "*.csproj" -o -name "*.fsproj" \) | while read -r project; do
  echo "Processing project: $project"
  dotnet format style --no-restore --severity info --verify-no-changes -v d "$project"
done
