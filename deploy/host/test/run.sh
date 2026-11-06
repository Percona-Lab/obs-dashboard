#!/usr/bin/env bash
# Tests apply.sh against stub podman/systemctl/curl in a temp tree.
set -euo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
APPLY="$HERE/../apply.sh"
BACKUP=pre-v0.2.0-20261106T120000Z.db
pass=0
failures=()

setup() {
  T=$(mktemp -d)
  export STUB_DIR="$T/stub" STUB_CALLS="$T/calls"
  export APPLY_DIR="$T/apply" DATA_DIR="$T/data"
  export DROPIN="$T/units/obs-dashboard.container.d/10-version.conf"
  export PODMAN="$HERE/stubs/podman" SYSTEMCTL="$HERE/stubs/systemctl" CURL="$HERE/stubs/curl"
  export HEALTH_TIMEOUT=1 HEALTH_INTERVAL=0.1
  unset STUB_PULL_FAIL STUB_START_FAIL STUB_HEALTH STUB_BAD_VERSION
  mkdir -p "$STUB_DIR" "$APPLY_DIR" "$DATA_DIR/backups" "$(dirname "$DROPIN")"
  printf '[Container]\nImage=ghcr.io/percona-lab/obs-dashboard:v0.1.0\n' >"$DROPIN"
  echo current-db >"$DATA_DIR/obsboard.db"
  echo wal >"$DATA_DIR/obsboard.db-wal"
  echo backup-db >"$DATA_DIR/backups/$BACKUP"
  echo v0.1.0 >"$STUB_DIR/running"
  : >"$STUB_CALLS"
}

request() { # <version> <backup>
  printf '{"version":"%s","previous":"v0.1.0","backup":"%s","requested_by":"a@percona.com","requested_at":"2026-11-06T12:00:00Z"}\n' \
    "$1" "$2" >"$APPLY_DIR/apply-request"
}

run_apply() { bash "$APPLY" >"$T/out" 2>&1; }
st() { jq -r "$1" "$APPLY_DIR/apply-status"; }
pin() { sed -n 's|^Image=.*:\(v[0-9.]*\)$|\1|p' "$DROPIN"; }
ok() { # <description> <command...>
  local d=$1
  shift
  if "$@"; then return 0; fi
  echo "    expected: $d"
  return 1
}
one_status() { [[ -f $APPLY_DIR/apply-status && ! -e $APPLY_DIR/apply-request ]]; }

case_success() {
  request v0.2.0 "$BACKUP"
  run_apply
  ok "one status, request removed" one_status &&
    ok "ok=true" [ "$(st .ok)" = true ] &&
    ok "version" [ "$(st .version)" = v0.2.0 ] &&
    ok "previous" [ "$(st .previous)" = v0.1.0 ] &&
    ok "pin v0.2.0" [ "$(pin)" = v0.2.0 ] &&
    ok "db untouched" [ "$(cat "$DATA_DIR/obsboard.db")" = current-db ] &&
    ok "pulled" grep -q 'podman pull ghcr.io/percona-lab/obs-dashboard:v0.2.0' "$STUB_CALLS" &&
    ok "restarted" grep -q 'systemctl restart obs-dashboard.service' "$STUB_CALLS" &&
    ok "pruned" grep -q 'podman image prune -f' "$STUB_CALLS" &&
    ok "no .prev left" [ ! -e "$DROPIN.prev" ]
}

case_invalid_version() {
  request 'v0.2;rm -rf /' "$BACKUP"
  run_apply
  ok "one status" one_status &&
    ok "ok=false" [ "$(st .ok)" = false ] &&
    ok "error" grep -q 'invalid version' <<<"$(st .error)" &&
    ok "no podman" bash -c "! grep -q podman '$STUB_CALLS'" &&
    ok "pin unchanged" [ "$(pin)" = v0.1.0 ]
}

case_backup_traversal() {
  request v0.2.0 '../obsboard.db'
  run_apply
  ok "one status" one_status && ok "error" grep -q 'invalid backup' <<<"$(st .error)" && ok "pin unchanged" [ "$(pin)" = v0.1.0 ]
}

case_missing_backup() {
  request v0.2.0 pre-v0.2.0-20261106T130000Z.db
  run_apply
  ok "one status" one_status && ok "error" grep -q 'backup not found' <<<"$(st .error)"
}

case_pull_failure() {
  export STUB_PULL_FAIL=1
  request v0.2.0 "$BACKUP"
  run_apply
  ok "one status" one_status &&
    ok "ok=false" [ "$(st .ok)" = false ] &&
    ok "error" grep -q 'pull failed' <<<"$(st .error)" &&
    ok "pin unchanged" [ "$(pin)" = v0.1.0 ] &&
    ok "no restart" bash -c "! grep -q 'systemctl restart' '$STUB_CALLS'"
}

assert_rolled_back() {
  ok "one status" one_status &&
    ok "ok=false" [ "$(st .ok)" = false ] &&
    ok "rolled back message" grep -q 'rolled back to v0.1.0$' <<<"$(st .error)" &&
    ok "pin restored" [ "$(pin)" = v0.1.0 ] &&
    ok "db restored" [ "$(cat "$DATA_DIR/obsboard.db")" = backup-db ] &&
    ok "wal removed" [ ! -e "$DATA_DIR/obsboard.db-wal" ] &&
    ok "previous running" [ "$(cat "$STUB_DIR/running")" = v0.1.0 ] &&
    ok "no .prev left" [ ! -e "$DROPIN.prev" ]
}

case_health_timeout() {
  export STUB_HEALTH=baddead STUB_BAD_VERSION=v0.2.0
  request v0.2.0 "$BACKUP"
  run_apply
  assert_rolled_back
}

case_wrong_version() {
  export STUB_HEALTH=badwrong STUB_BAD_VERSION=v0.2.0
  request v0.2.0 "$BACKUP"
  run_apply
  assert_rolled_back
}

case_rollback_fails() {
  export STUB_HEALTH=baddead STUB_BAD_VERSION=v0.2.0 STUB_START_FAIL=1
  request v0.2.0 "$BACKUP"
  run_apply
  ok "one status" one_status && ok "rollback failed" grep -q 'rollback failed' <<<"$(st .error)"
}

case_no_request() {
  run_apply
  ok "no status" [ ! -e "$APPLY_DIR/apply-status" ]
}

for c in success invalid_version backup_traversal missing_backup pull_failure health_timeout wrong_version rollback_fails no_request; do
  setup
  if "case_$c"; then
    pass=$((pass + 1))
    echo "ok   $c"
  else
    failures+=("$c")
    echo "FAIL $c"
    sed 's/^/    | /' "$T/out"
  fi
  rm -rf "$T"
done

if ((${#failures[@]})); then
  echo "${#failures[@]} case(s) failed: ${failures[*]}"
  exit 1
fi
echo "all $pass cases passed"
