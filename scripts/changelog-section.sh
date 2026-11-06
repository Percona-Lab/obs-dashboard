#!/usr/bin/env bash
# Prints the body of a CHANGELOG.md section (without its "## [x]" heading),
# trimmed of surrounding blank lines. Usage: changelog-section.sh vX.Y.Z|X.Y.Z|Unreleased [file]
set -euo pipefail
v=${1:?usage: changelog-section.sh vX.Y.Z|Unreleased [CHANGELOG.md]}
v=${v#v}
file=${2:-CHANGELOG.md}
awk -v v="$v" '
  /^## \[/ { if (insec) exit; if (index($0, "## [" v "]") == 1) { insec = 1; next } }
  insec { lines[++n] = $0 }
  END {
    s = 1; while (s <= n && lines[s] ~ /^[[:space:]]*$/) s++
    e = n; while (e >= s && lines[e] ~ /^[[:space:]]*$/) e--
    for (i = s; i <= e; i++) print lines[i]
  }
' "$file"
