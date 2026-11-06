#!/usr/bin/env bash
# Applies an obs-dashboard upgrade requested from the dashboard UI.
#
# Run by obs-dashboard-apply.service when the dashboard writes
# $APPLY_DIR/apply-request. Pulls the requested image, re-pins the Quadlet
# drop-in, restarts the service and waits for /healthz to report the new
# version. On failure after the switch it restores the previous pin and the
# pre-upgrade database backup. Always writes exactly one apply-status and
# removes the request. Every request field is untrusted; the image repository
# is fixed here and never read from the request.
#
# All paths and commands can be overridden through the environment for tests.
set -euo pipefail

APPLY_DIR=${APPLY_DIR:-/etc/obs-dashboard/apply}
DATA_DIR=${DATA_DIR:-/var/lib/obs-dashboard}
DROPIN=${DROPIN:-/etc/containers/systemd/obs-dashboard.container.d/10-version.conf}
UNIT=${UNIT:-obs-dashboard.service}
IMAGE_REPO=${IMAGE_REPO:-ghcr.io/percona-lab/obs-dashboard}
HEALTH_URL=${HEALTH_URL:-http://127.0.0.1:4000/healthz}
HEALTH_TIMEOUT=${HEALTH_TIMEOUT:-120}
HEALTH_INTERVAL=${HEALTH_INTERVAL:-2}
PODMAN=${PODMAN:-podman}
SYSTEMCTL=${SYSTEMCTL:-systemctl}
CURL=${CURL:-curl}

REQ="$APPLY_DIR/apply-request"
STATUS="$APPLY_DIR/apply-status"
VERSION_RE='^v[0-9]+\.[0-9]+\.[0-9]+$'
BACKUP_RE='^pre-v[0-9]+\.[0-9]+\.[0-9]+-[0-9]{8}T[0-9]{6}Z\.db$'

VERSION=""
PREVIOUS=""
BACKUP=""
SWITCHED=0
DONE=0

log() { echo "apply: $*"; }

write_status() { # <true|false> <error>
  local tmp
  tmp=$(mktemp "$APPLY_DIR/.apply-status.XXXXXX")
  jq -n --arg v "$VERSION" --arg p "$PREVIOUS" --argjson ok "$1" --arg e "$2" \
    --arg t "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    '{version: $v, previous: $p, ok: $ok, error: $e, finished_at: $t}' >"$tmp"
  chmod 0644 "$tmp"
  mv -f "$tmp" "$STATUS"
  rm -f "$REQ"
  DONE=1
  if [[ $1 == true ]]; then log "done: running $VERSION"; else log "failed: $2"; fi
}

fail() { # <error>: report a failure before anything was switched
  write_status false "$1"
  exit 0
}

pinned_version() {
  sed -n 's|^Image=.*:\(v[0-9][0-9.]*\)[[:space:]]*$|\1|p' "$DROPIN" | tail -n 1
}

write_pin() { # <version>
  local tmp
  tmp=$(mktemp "$(dirname "$DROPIN")/.10-version.XXXXXX")
  printf '[Container]\nImage=%s:%s\n' "$IMAGE_REPO" "$1" >"$tmp"
  chmod 0644 "$tmp"
  mv -f "$tmp" "$DROPIN"
}

# backup_ok: the backup is a regular, non-symlink file that resolves inside
# $DATA_DIR/backups, and that directory is not itself a symlink. The container
# can write $DATA_DIR, so none of these may be assumed.
backup_ok() {
  local dir="$DATA_DIR/backups" f="$DATA_DIR/backups/$BACKUP" real
  [[ -d $dir && ! -L $dir && -f $f && ! -L $f ]] || return 1
  real=$(realpath -e -- "$f") || return 1
  [[ $real == "$(realpath -e -- "$dir")/$BACKUP" ]]
}

wait_healthy() { # <version>
  local deadline=$((SECONDS + HEALTH_TIMEOUT)) body got
  while ((SECONDS < deadline)); do
    if body=$("$CURL" -fsS --max-time 5 "$HEALTH_URL" 2>/dev/null); then
      got=$(jq -r '.version // empty' <<<"$body" 2>/dev/null || true)
      [[ $got == "$1" ]] && return 0
    fi
    sleep "$HEALTH_INTERVAL"
  done
  return 1
}

rollback() { # <reason>
  set +e
  local problems=() db="$DATA_DIR/obsboard.db" stopped=1 tmp=""
  log "rolling back to $PREVIOUS: $1"
  "$SYSTEMCTL" stop "$UNIT" || { problems+=("stop failed"); stopped=0; }
  if ((stopped)); then
    if tmp=$(mktemp "$DATA_DIR/.obsboard.db.XXXXXX") && backup_ok &&
      cp -f "$DATA_DIR/backups/$BACKUP" "$tmp" && mv -f "$tmp" "$db"; then
      rm -f "$db-wal" "$db-shm"
    else
      [[ -n $tmp ]] && rm -f "$tmp"
      problems+=("database restore failed")
    fi
  fi
  if [[ -f $DROPIN.prev ]]; then
    mv -f "$DROPIN.prev" "$DROPIN" || problems+=("pin restore failed")
  fi
  "$SYSTEMCTL" daemon-reload || problems+=("daemon-reload failed")
  if ! "$SYSTEMCTL" start "$UNIT"; then
    problems+=("start failed")
  elif ! wait_healthy "$PREVIOUS"; then
    problems+=("$PREVIOUS not healthy within ${HEALTH_TIMEOUT}s")
  fi
  if ((${#problems[@]})); then
    local joined
    joined=$(printf '%s, ' "${problems[@]}")
    write_status false "$1; rollback failed: ${joined%, }"
  else
    write_status false "$1; rolled back to $PREVIOUS"
  fi
}

on_exit() {
  local rc=$?
  ((DONE)) && return
  if ((SWITCHED)); then
    rollback "apply.sh failed unexpectedly (exit $rc)"
  else
    write_status false "apply.sh failed unexpectedly (exit $rc)"
  fi
}

if [[ ! -f $REQ ]]; then
  log "no apply-request; nothing to do"
  exit 0
fi
trap on_exit EXIT

if [[ -L $REQ ]]; then
  rm -f "$REQ"
  fail "apply-request must not be a symlink"
fi

VERSION=$(jq -r '.version // "" | tostring' "$REQ" 2>/dev/null) || VERSION=""
BACKUP=$(jq -r '.backup // "" | tostring' "$REQ" 2>/dev/null) || BACKUP=""
[[ -f $DROPIN ]] || fail "version drop-in $DROPIN is missing"
PREVIOUS=$(pinned_version)
[[ $PREVIOUS =~ $VERSION_RE ]] || fail "cannot read the running version from $DROPIN"
[[ $VERSION =~ $VERSION_RE ]] || fail "invalid version in request: '$VERSION'"
[[ $BACKUP =~ $BACKUP_RE ]] || fail "invalid backup name in request: '$BACKUP'"
[[ -e $DATA_DIR/backups/$BACKUP || -L $DATA_DIR/backups/$BACKUP ]] || fail "backup not found: $BACKUP"
backup_ok || fail "backup is not a regular file inside the backups directory: $BACKUP"
[[ $VERSION != "$PREVIOUS" ]] || fail "$VERSION is already running"

log "upgrading $PREVIOUS -> $VERSION (backup $BACKUP)"
"$PODMAN" pull "$IMAGE_REPO:$VERSION" || fail "image pull failed for $IMAGE_REPO:$VERSION; still running $PREVIOUS"

cp -p "$DROPIN" "$DROPIN.prev"
SWITCHED=1
write_pin "$VERSION"
"$SYSTEMCTL" daemon-reload
"$SYSTEMCTL" restart "$UNIT"
if ! wait_healthy "$VERSION"; then
  rollback "$VERSION did not report healthy within ${HEALTH_TIMEOUT}s"
  exit 0
fi
rm -f "$DROPIN.prev"
"$PODMAN" image prune -f >/dev/null 2>&1 || true
write_status true ""
