#!/usr/bin/env sh
set -eou pipefail

# Handle --version
if [ "${1:-}" = "--version" ]; then
  echo "ℹ️ dn-inspect 0.4.0"
  echo "using dotnet: $("${DN_INSPECT_DOTNET:+$DN_INSPECT_DOTNET/bin/}dotnet" --version)"
  exit 0
fi

# The bundled JetBrains command-line tool (`dotnet jb`) targets an older .NET
# runtime than the SDK we now bundle for `.slnx` support, so allow the host to
# roll forward to the bundled major. Respect an explicit caller override.
export DOTNET_ROLL_FORWARD="${DOTNET_ROLL_FORWARD:-Major}"

# Auto-discover a solution in the current directory. Both `.sln` (legacy) and
# `.slnx` (modern) are supported. Both-present rule: prefer `.slnx`, the modern
# canonical format, so behaviour is deterministic when both exist.
solution=$(find . -maxdepth 1 -name '*.slnx' -print -quit 2>/dev/null)
[ -z "$solution" ] && solution=$(find . -maxdepth 1 -name '*.sln' -print -quit 2>/dev/null)
solution="${solution#./}"
filter=""
use_projects=false
no_build=false

# Accumulate argument lists that may contain whitespace (project paths, include
# globs) as eval-safe, single-quoted tokens rather than a space-delimited string.
# A space-delimited string expanded unquoted word-splits a path like
# `Space Name.slnx` into two arguments, so `dotnet jb inspectcode` aborts with
# "Specify only one solution file". These are rebuilt into positional parameters
# with `eval set --` at the point of use, preserving each argument intact.
project_args=""
include_args=""

# Wrap $1 in single quotes (escaping any embedded single quote) so the result is
# safe to reconstruct with `eval`. POSIX sh has a single array — the positional
# parameters — so this is the portable way to carry a list of whitespace-bearing
# values until they are turned back into "$@".
quote_arg() {
  printf "'%s'" "$(printf '%s' "$1" | sed "s/'/'\\\\''/g")"
}

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
    include_args="$include_args $(quote_arg "--include=${1#--include=}")"
    shift
    ;;
  --include)
    shift
    [ -z "${1:-}" ] && echo "❌ Error: --include requires a pattern" >&2 && exit 1
    include_args="$include_args $(quote_arg "--include=$1")"
    shift
    ;;
  --projects)
    shift
    [ "$#" -eq 0 ] && echo "❌ Error: --projects requires at least one project" >&2 && exit 1
    use_projects=true
    # Track whether at least one real project token was collected: the loop below
    # stops at the next option, so `--projects --no-build` would otherwise set
    # use_projects=true with an empty list and inspect an empty temp solution.
    has_project=false
    while [ "$#" -gt 0 ] && [ "${1#--}" = "$1" ]; do
      has_project=true
      project_args="$project_args $(quote_arg "$1")"
      shift
    done
    if [ "$has_project" != true ]; then
      echo "❌ Error: --projects requires at least one project" >&2
      exit 1
    fi
    ;;
  *.sln | *.slnx)
    solution="$1"
    shift
    ;;
  *)
    echo "❌ Error: Unknown argument: $1" >&2
    exit 1
    ;;
  esac
done

# Project-only mode fabricates its own temp solution, so it must not require a
# discovered one — nor validate one. Both the empty-solution guard and the
# file-existence guard are skipped under --projects: auto-discovery may surface a
# broken/missing solution (e.g. a dangling `*.slnx` symlink) that project mode
# would otherwise reject before reaching temp-solution creation. The discovered
# solution is overwritten by the temp solution in project mode anyway. Keep both
# guards for every other invocation.
if [ "$use_projects" != true ]; then
  [ -z "$solution" ] && echo "❌ Error: No .sln/.slnx file found in current directory" >&2 && exit 1
  [ -n "$solution" ] && [ ! -f "$solution" ] && echo "❌ Error: File not found: $solution" >&2 && exit 1
fi

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
  # Force the legacy `.sln` format so the created file's extension matches the
  # `.sln` name used below — on a `.slnx`-defaulting SDK (.NET 10) `dotnet new
  # sln` would otherwise emit a `.slnx` and the subsequent `add`/inspect on
  # `$sln_file` (a `.sln`) would fail.
  dotnet new sln --format sln --output "$(dirname "$sln_file")" --name "$(basename "$sln_file" .sln)" >/dev/null
  # Rebuild the project list into positional parameters so each path (which may
  # contain whitespace) is added as a single argument.
  eval "set -- $project_args"
  for project in "$@"; do
    echo "➕ Adding project: $project"
    dotnet sln "$sln_file" add "$project" >/dev/null
  done
  echo "✅ Temp solution ready"
fi

trap '[ "$cleanup_report" = false ] || rm -f "$report_file"; [ "$cleanup_sln_dir" = false ] || [ -z "${sln_dir:-}" ] || rm -rf "$sln_dir"; rm -f "${inspect_err:-}"' EXIT

if [ -n "$filter" ]; then
  echo "🔍 Running inspectcode with filter: $filter"
else
  echo "🔍 Running inspectcode"
fi

# Build the inspectcode invocation in positional parameters so the solution path
# and every include glob are passed as single, intact arguments even when they
# contain whitespace (see quote_arg above).
set -- "$sln_file" "--format=Sarif" "--output=$report_file"
[ "$no_build" = true ] && set -- "$@" "--no-build"
[ -n "$include_args" ] && eval "set -- \"\$@\" $include_args"

# ReSharper's analyzer host runs on the rolled-forward .NET 10 runtime, where the
# SDK's own bundled Roslyn analyzers / source generators fail to instantiate
# (`System.Composition.AttributedModel` not found) and flood *stderr* with
# hundreds of non-fatal exception dumps per run. stdout and the SARIF report are
# unaffected, and a genuine build/inspection failure is signalled by `dotnet jb`'s
# non-zero exit code — not by this stderr text. So: capture stderr; on success
# drop only that known-benign analyzer-load noise; on failure pass stderr through
# untouched and propagate the exit code, so nothing is ever hidden when something
# actually breaks.
#
# Suppression is BLOCK-SCOPED, not a global line-level blacklist: a block is
# dropped only when it carries BOTH benign anchors — the `Roslyn error
# ProviderInstantiation` headline AND the missing `System.Composition.AttributedModel`
# assembly. The noise arrives in two shapes, both handled by the awk filter below:
#   1. a single condensed `Roslyn error ProviderInstantiation …` line, and
#   2. a multi-line `--- EXCEPTION … [LoggerException]` dump terminated by a blank line.
# Any block missing either anchor — a consumer's OWN analyzer failing to load
# (different assembly name), a real `CSxxxx`/`MSBxxxx` diagnostic, an unrelated
# exception — is passed through verbatim. This is deliberately conservative:
# under-suppression (showing harmless extra lines) is preferred over hiding a real
# diagnostic. The filter is tied to the ReSharper 2026.1.3 + SDK 10 dump format and
# should be revisited on the next toolchain bump.
inspect_err="$(mktemp "${base_tmp_dir%/}/dn-inspect-stderr.XXXXXX")"
# This is an awk program; `$0`/fields are awk syntax, not shell expansions.
# shellcheck disable=SC2016
inspect_filter='
function flush(   i) {
  # Print the buffered LoggerException block unless it is the known-benign one
  # (both anchors present); either way reset the block state.
  if (!(blk_roslyn && blk_assembly)) { for (i = 1; i <= n; i++) print buf[i]; dropped = 0 }
  else { dropped = 1 }
  n = 0; blk_roslyn = 0; blk_assembly = 0; in_blk = 0
}
{
  if (in_blk) {
    buf[++n] = $0
    if (index($0, "Roslyn error ProviderInstantiation")) blk_roslyn = 1
    if (index($0, "System.Composition.AttributedModel")) blk_assembly = 1
    if ($0 == "") flush()
    next
  }
  if ($0 ~ /^--- EXCEPTION .*\[LoggerException\]/) {
    in_blk = 1; n = 0; blk_roslyn = 0; blk_assembly = 0; buf[++n] = $0; next
  }
  if ($0 ~ /^Roslyn error ProviderInstantiation/ && index($0, "System.Composition.AttributedModel")) {
    dropped = 1; next
  }
  if ($0 == "" && dropped) next       # collapse blanks left behind by dropped noise
  dropped = 0
  print
}
END { if (in_blk) flush() }
'
set +e
dotnet jb inspectcode "$@" 2>"$inspect_err"
inspect_ec=$?
set -e
if [ "$inspect_ec" -eq 0 ]; then
  awk "$inspect_filter" "$inspect_err" >&2
else
  cat "$inspect_err" >&2
fi
rm -f "$inspect_err"
[ "$inspect_ec" -eq 0 ] || exit "$inspect_ec"

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
