#!/usr/bin/env bash
# Tests changelog-section.sh and check-release.sh in throwaway git repos.
set -euo pipefail
SCRIPTS=$(cd "$(dirname "$0")/.." && pwd)
pass=0
failures=()

repo() { # fresh repo with scripts and a deploy/host file
  R=$(mktemp -d)
  git -C "$R" init -q
  git -C "$R" config user.email t@example.com
  git -C "$R" config user.name t
  mkdir -p "$R/scripts" "$R/deploy/host/test"
  cp "$SCRIPTS/changelog-section.sh" "$SCRIPTS/check-release.sh" "$R/scripts/"
  echo a >"$R/deploy/host/apply.sh"
  echo t >"$R/deploy/host/test/run.sh"
}
commit_tag() { # <tag> <changelog content>
  printf '%s\n' "$2" >"$R/CHANGELOG.md"
  git -C "$R" add -A
  git -C "$R" commit -q -m "$1"
  git -C "$R" tag -a "$1" -m "$1"
}
check() { (cd "$R" && scripts/check-release.sh "$1") >/dev/null 2>&1; }

CL1='# Changelog

## [Unreleased]

## [0.1.0] - 2026-11-06

### Added

- first

### Host changes

- install host files
'

case_section() {
  repo
  printf '%s' "$CL1" >"$R/CHANGELOG.md"
  local out
  out=$(cd "$R" && scripts/changelog-section.sh v0.1.0)
  [[ $(head -n 1 <<<"$out") == "### Added" && $(tail -n 1 <<<"$out") == "- install host files" ]] &&
    [[ -z $(cd "$R" && scripts/changelog-section.sh v9.9.9) ]]
}
case_first_tag_ok() { repo; commit_tag v0.1.0 "$CL1"; check v0.1.0; }
case_first_tag_needs_host_changes() {
  repo
  commit_tag v0.1.0 '# Changelog

## [0.1.0] - 2026-11-06

- first'
  ! check v0.1.0
}
case_missing_section() { repo; commit_tag v0.1.0 '# Changelog'; ! check v0.1.0; }
case_host_change_without_note() {
  repo
  commit_tag v0.1.0 "$CL1"
  echo b >"$R/deploy/host/apply.sh"
  commit_tag v0.2.0 "$CL1
## [0.2.0] - 2026-11-07

- changed apply"
  ! check v0.2.0
}
case_host_change_with_note() {
  repo
  commit_tag v0.1.0 "$CL1"
  echo b >"$R/deploy/host/apply.sh"
  commit_tag v0.2.0 "## [0.2.0] - 2026-11-07

#### HOST CHANGES

- reinstall apply.sh
$CL1"
  check v0.2.0
}
case_only_tests_changed() {
  repo
  commit_tag v0.1.0 "$CL1"
  echo u >"$R/deploy/host/test/run.sh"
  commit_tag v0.2.0 "## [0.2.0] - 2026-11-07

- test only
$CL1"
  check v0.2.0
}

for c in section first_tag_ok first_tag_needs_host_changes missing_section host_change_without_note host_change_with_note only_tests_changed; do
  if "case_$c"; then pass=$((pass + 1)); echo "ok   $c"; else failures+=("$c"); echo "FAIL $c"; fi
  rm -rf "${R:-/nonexistent}"
done
if ((${#failures[@]})); then echo "${#failures[@]} failed: ${failures[*]}"; exit 1; fi
echo "all $pass cases passed"
