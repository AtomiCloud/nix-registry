# shellcheck shell=bash
# The consumer owns its source and vendor hash; this wrapper owns the offline
# Go runtime contract shared by lint and dead-code commands.
set -euo pipefail

usage() {
  cat >&2 <<'EOF'
Usage: go-validator run --proxy <buildGoModule.goModules directory> -- <command> [args...]
EOF
  exit 2
}

refuse() {
  printf '❌ go-validator: %s\n' "$1" >&2
  exit 1
}

case "${1:-}" in
run) shift ;;
--version)
  printf 'go-validator %s\n' "${GO_VALIDATOR_VERSION}"
  exit 0
  ;;
-h | --help | '') usage ;;
*) refuse "unknown command '${1}'; expected 'run', '--help', or '--version'" ;;
esac

[ "${1:-}" = "--proxy" ] || refuse "'run' requires '--proxy <buildGoModule.goModules directory>'"
[ -n "${2:-}" ] || refuse "'--proxy' needs a buildGoModule.goModules directory"
proxy="$2"
shift 2
[ -d "${proxy}" ] || refuse "declared GOPROXY directory '${proxy}' does not exist"
[ "${1:-}" = "--" ] || refuse "'run' needs '--' before the validator command"
shift
[ "$#" -gt 0 ] || refuse "'run' needs a validator command after '--'"

cache="${TMPDIR:-/tmp}/go-validator-mod-cache"
mkdir -p "${cache}" || refuse "could not create module cache '${cache}'"
export CGO_ENABLED=0 GOPROXY="file://${proxy}" GOSUMDB=off GOMODCACHE="${cache}"
exec "$@"
