#!/usr/bin/env bash
# Cuts release X.Y.Z locally: moves CHANGELOG "Unreleased" into a dated
# section, bumps frontend/package.json, commits (signed off) and tags vX.Y.Z.
# Never pushes.
set -euo pipefail
die() { echo "release: $*" >&2; exit 1; }
v=${1:?usage: release.sh X.Y.Z}
v=${v#v}
[[ $v =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || die "version must be X.Y.Z, got $v"
cd "$(git rev-parse --show-toplevel)"
[[ -z $(git status --porcelain) ]] || die "working tree is not clean"
! git rev-parse -q --verify "refs/tags/v$v" >/dev/null || die "tag v$v already exists"
grep -q '^## \[Unreleased\]' CHANGELOG.md || die "CHANGELOG.md has no '## [Unreleased]' section"
[[ -n $(scripts/changelog-section.sh Unreleased) ]] || die "the Unreleased section is empty"
awk -v v="$v" -v d="$(date -u +%Y-%m-%d)" '
  /^## \[Unreleased\]/ && !done { print; print ""; print "## [" v "] - " d; done = 1; next }
  { print }
' CHANGELOG.md >CHANGELOG.md.tmp
mv CHANGELOG.md.tmp CHANGELOG.md
(cd frontend && npm version "$v" --no-git-tag-version --allow-same-version >/dev/null)
git add CHANGELOG.md frontend/package.json frontend/package-lock.json
git commit -s -q -m "release: v$v"
git tag -a "v$v" -m "v$v"
if ! scripts/check-release.sh "v$v"; then
  echo "release: check failed; undo with: git tag -d v$v && git reset --hard HEAD~1" >&2
  exit 1
fi
echo "Tagged v$v. Push with: git push origin main v$v"
