#!/usr/bin/env sh
set -eou pipefail

# Handle --version
[ "${1:-}" = "--version" ] && echo "ℹ️ dn-inspect 0.2.0" && exit 0

# Find .sln in current directory
solution=$(find . -maxdepth 1 -name '*.sln' -print -quit 2>/dev/null)
solution="${solution#./}"
filter=""
use_projects=false
projects=""
no_build=false
include_patterns=""

while [ "$#" -gt 0 ]; do
  case "$1" in
  --filter)
    shift
    [ -z "${1:-}" ] && echo "❌ Error: --filter requires a pattern" >&2 && exit 1
    filter="$1"
    shift
    ;;
  --no-build)
    no_build=true
    shift
    ;;
  --include=*)
    include_patterns="$include_patterns --include=${1#--include=}"
    shift
    ;;
  --include)
    shift
    [ -z "${1:-}" ] && echo "❌ Error: --include requires a pattern" >&2 && exit 1
    include_patterns="$include_patterns --include=$1"
    shift
    ;;
  --projects)
    shift
    [ "$#" -eq 0 ] && echo "❌ Error: --projects requires at least one project" >&2 && exit 1
    use_projects=true
    while [ "$#" -gt 0 ] && [ "${1#--}" = "$1" ]; do
      projects="$projects $1"
      shift
    done
    ;;
  *.sln)
    solution="$1"
    shift
    ;;
  *)
    echo "❌ Error: Unknown argument: $1" >&2
    exit 1
    ;;
  esac
done

[ -z "$solution" ] && echo "❌ Error: No .sln file found in current directory" >&2 && exit 1
[ ! -f "$solution" ] && echo "❌ Error: File not found: $solution" >&2 && exit 1

base_tmp_dir="${TMPDIR:-/tmp}"
if [ -n "${DN_INSPECT_REPORT_FILE:-}" ]; then
  report_file="$DN_INSPECT_REPORT_FILE"
  cleanup_report=false
else
  report_file="$(mktemp "${base_tmp_dir%/}/dn-inspect-report.XXXXXX.sarif")"
  cleanup_report=true
fi

sln_file="$solution"
cleanup_sln_dir=false
if [ "$use_projects" = true ]; then
  if [ -n "${DN_INSPECT_TEMP_SLN:-}" ]; then
    sln_dir=""
    sln_file="$DN_INSPECT_TEMP_SLN"
  else
    sln_dir="$(mktemp -d "${base_tmp_dir%/}/dn-inspect-sln.XXXXXX")"
    sln_file="$sln_dir/dn-inspect.sln"
    cleanup_sln_dir=true
  fi
  echo "🧩 Creating temp solution: $sln_file"
  dotnet new sln --output "$(dirname "$sln_file")" --name "$(basename "$sln_file" .sln)" >/dev/null
  for project in $projects; do
    echo "➕ Adding project: $project"
    dotnet sln "$sln_file" add "$project" >/dev/null
  done
  echo "✅ Temp solution ready"
fi

trap '[ "$cleanup_report" = false ] || rm -f "$report_file"; [ "$cleanup_sln_dir" = false ] || [ -z "${sln_dir:-}" ] || rm -rf "$sln_dir"' EXIT

if [ -n "$filter" ]; then
  echo "🔍 Running inspectcode with filter: $filter"
else
  echo "🔍 Running inspectcode"
fi

inspectcode_args="$sln_file --format=Sarif --output=$report_file"
[ "$no_build" = true ] && inspectcode_args="$inspectcode_args --no-build"
[ -n "$include_patterns" ] && inspectcode_args="$inspectcode_args$include_patterns"

# shellcheck disable=SC2086
dotnet jb inspectcode $inspectcode_args

[ ! -s "$report_file" ] && echo "✅ No SARIF output produced" && echo "📊 Total: 0 issue(s)" && exit 0

if [ -n "$filter" ]; then
  issues_json=$(jq -c --arg filter "$filter" '.runs[].results[] | {ruleId: .ruleId, message: .message.text, location: (.locations[0].physicalLocation)} | select(.ruleId | test($filter))' "$report_file" 2>/dev/null || true)
else
  issues_json=$(jq -c '.runs[].results[] | {ruleId: .ruleId, message: .message.text, location: (.locations[0].physicalLocation)}' "$report_file" 2>/dev/null || true)
fi

[ -z "$issues_json" ] && echo "✅ No issues found" && echo "📊 Total: 0 issue(s)" && exit 0

# Store issues in temp file for two-pass processing
issues_tmp=$(mktemp)
printf "%s\n" "$issues_json" >"$issues_tmp"

# Calculate max widths
max_file=4 max_rule=4 max_msg=7
while IFS= read -r issue; do
  rule=$(printf "%s" "$issue" | jq -r '.ruleId // ""')
  file=$(printf "%s" "$issue" | jq -r '.location.artifactLocation.uri // ""')
  message=$(printf "%s" "$issue" | jq -r '.message // ""')
  [ ${#file} -gt "$max_file" ] && max_file=${#file}
  [ ${#rule} -gt "$max_rule" ] && max_rule=${#rule}
  [ ${#message} -gt "$max_msg" ] && max_msg=${#message}
done <"$issues_tmp"
rm -f "$issues_tmp"

# Print table
echo ""
printf "%-${max_file}s  %-6s  %-${max_rule}s  %s\n" "File" "Line" "Rule" "Message"
separator=$(printf '%*s' $((max_file + max_rule + max_msg + 20)) '' | tr ' ' '-')
printf "%s\n" "$separator"

count=0
printf "%s\n" "$issues_json" | while read -r issue; do
  rule=$(printf "%s" "$issue" | jq -r '.ruleId // ""')
  file=$(printf "%s" "$issue" | jq -r '.location.artifactLocation.uri // ""')
  line=$(printf "%s" "$issue" | jq -r '.location.region.startLine // ""')
  message=$(printf "%s" "$issue" | jq -r '.message // ""')
  printf "%-${max_file}s  %-6s  %-${max_rule}s  %s\n" "$file" "$line" "$rule" "$message"
  count=$((count + 1))
done
echo ""

count=$(printf "%s\n" "$issues_json" | wc -l)
[ "$count" -gt 0 ] && echo "📊 Total: $count issue(s)" && exit 1
echo "✅ No issues found" && echo "📊 Total: 0 issue(s)"
