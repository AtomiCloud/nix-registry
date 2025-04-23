#!/usr/bin/env bash
set -euo pipefail

echo "Recursively searching for Helm charts..."

# Find all directories containing a Chart.yaml file
find . -type f -name "Chart.yaml" | while read -r chartfile; do
  chart_dir=$(dirname "$chartfile")
  echo "Processing chart: $chart_dir"

  helm lint -f "$chart_dir/values.yaml" "$chart_dir"
done
