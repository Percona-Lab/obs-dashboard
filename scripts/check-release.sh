#!/usr/bin/env bash
# Release gate for tag vX.Y.Z: CHANGELOG.md must have a non-empty section for
# it, with a "Host changes" subsection whenever deploy/host/ (excluding
# test/) changed since the previous v* tag, or exists at the first tag.
set -euo pipefail
tag=${1:?usage: check-release.sh vX.Y.Z}
[[ $tag =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "not a vX.Y.Z tag: $tag" >&2; exit 1; }
here=$(cd "$(dirname "$0")" && pwd)
section=$(git show "$tag:CHANGELOG.md" 2>/dev/null | "$here/changelog-section.sh" "$tag" /dev/stdin || true)
[[ -n $section ]] || { echo "CHANGELOG.md at $tag has no section for ${tag#v}" >&2; exit 1; }
prev=$(git describe --tags --abbrev=0 --match 'v[0-9]*' "$tag^" 2>/dev/null || true)
if [[ -n $prev ]]; then
  changed=$(git diff --name-only "$prev" "$tag" -- deploy/host ':(exclude)deploy/host/test')
else
  changed=$(git ls-tree -r --name-only "$tag" -- deploy/host | grep -v '^deploy/host/test/' || true)
fi
if [[ -n $changed ]] && ! grep -Eqi '^#{2,4}[[:space:]]+host changes[[:space:]]*$' <<<"$section"; then
  echo "deploy/host/ changed since ${prev:-the first commit}, but the ${tag#v} section has no 'Host changes' subsection. Changed:" >&2
  echo "$changed" >&2
  exit 1
fi
echo "release check OK for $tag"
