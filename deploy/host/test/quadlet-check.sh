#!/usr/bin/env bash
# Dry-runs the Quadlet generator on obs-dashboard.container with a sample pin.
set -euo pipefail
here=$(cd "$(dirname "$0")/.." && pwd)
q=""
for c in /usr/libexec/podman/quadlet /usr/lib/podman/quadlet; do
  if [[ -x $c ]]; then q=$c; break; fi
done
[[ -n $q ]] || { echo "quadlet generator not found (install podman)" >&2; exit 1; }
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/units"
# Inline the pin the drop-in would provide, so the check does not depend on
# the CI podman's drop-in support.
sed '/^\[Container\]/a Image=ghcr.io/percona-lab/obs-dashboard:v0.0.0' \
  "$here/obs-dashboard.container" >"$work/units/obs-dashboard.container"
QUADLET_UNIT_DIRS="$work/units" "$q" -dryrun >"$work/out" 2>"$work/err" || { cat "$work/err" >&2; exit 1; }
for want in 'ghcr.io/percona-lab/obs-dashboard:v0.0.0' '127.0.0.1:4000:4000' ':/data' ':/apply' 'UPGRADE_APPLY_DIR=/apply'; do
  grep -q -- "$want" "$work/out" || { echo "generated unit lacks: $want" >&2; cat "$work/out" >&2; exit 1; }
done
echo "quadlet: OK"
