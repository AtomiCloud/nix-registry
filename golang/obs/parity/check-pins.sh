#!/usr/bin/env bash
set -euo pipefail
here=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
pins=${1:-"$here/../src/internal/pins/pins.json"}
images=${2:-"$here/images.json"}
# Check both directions, including commits. The actual container invocations read this
# image list; a hand-edited tag cannot bypass the check by retaining its version field.
jq -en --slurpfile pins "$pins" --slurpfile images "$images" '
  ($pins[0] | keys) == ($images[0] | keys) and
  all($pins[0] | to_entries[];
    .key as $key | .value as $pin | $images[0][$key] as $job |
    $pin.version == $job.version and $pin.commit == $job.commit and
    (if $key == "VictoriaLogsDatasource" then
      $job.plugin == ("victoriametrics-logs-datasource@" + ($pin.version | ltrimstr("v")))
    else
      ($job.image | split(":") | last) == $pin.version and
      ($job.digest | test("^sha256:[a-f0-9]{64}$")) and
      $pin.digest == $job.digest
    end))
' >/dev/null || {
  echo "parity images.json: engine version, commit, image tag, digest or datasource plugin differs from obs pins.json" >&2
  exit 1
}
echo "Parity pins match (chart pins NOT TESTED; no chart pin location is registered)."
