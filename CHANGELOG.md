# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/). Versions are plain
`vX.Y.Z` tags.

A release that changes anything under `deploy/host/` must include a
`### Host changes` subsection. It tells the operator what changed on the host
and any new `/etc/obs-dashboard/env` variable or `config.yaml` setting, with
an example value. See `deploy/host/README.md`.

## [Unreleased]

### Added

- Release tab for cutting PPG releases through GitHub. Off unless
  `RELEASE_GITHUB_TOKEN` is set; see the README.

## [0.1.2] - 2026-11-10

### Fixed

- Artifacts download links that returned 404 (PG-2860). Debian packages now
  link to the Debian arch folder (`amd64/`, `arm64/`, `all/`) and `noarch`
  RPMs to `noarch/`, instead of the build arch. Tarball links use the
  published filename from OBS instead of one rebuilt from the package version.

## [0.1.1] - 2026-11-09

### Added

- **Rescan affected** button in the "CVE exposure by project" header, for
  signed-in users. It queues a fresh scan of every image that currently has
  HIGH or CRITICAL CVEs, only for the affected repos. A rescan that is
  already pending is not queued again.

## [0.1.0] - 2026-11-06

### Added

- Upgrades from **Settings → Service**: newer GitHub releases are listed with
  their notes, and a signed-in user can apply one. The host applies it, checks
  `/healthz` and rolls back automatically if the new version is unhealthy.
- `GET /healthz`, which reports the running version.
- A banner, shown when a release needs new host files before it can be
  applied, with the manual steps.

### Host changes

- First release with UI upgrades. Install the host files and create
  `/etc/obs-dashboard/env` as described in `deploy/host/README.md`, including
  `OBS_DASHBOARD_HOST_FILES_VERSION=v0.1.0`.
