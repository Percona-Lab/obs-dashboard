# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/). Versions are plain
`vX.Y.Z` tags.

A release that changes anything under `deploy/host/` must include a
`### Host changes` subsection. It tells the operator what changed on the host
and any new `/etc/obs-dashboard/env` variable or `config.yaml` setting, with
an example value. See `deploy/host/README.md`.

## [Unreleased]

### Changed

- The release wizard's step bar marks finished steps with a tick, highlights
  the current step, and a status line below it says which step this is
  ("Step 3 of 5 · Pre-release sync") and what is happening: waiting on
  GitHub, your turn, or failed.

- Every place the release wizard waits now shows a spinner: the stage of the
  cut in progress, the sync and the final sync before they start, instances
  and QA lanes that are queued or running, QA before its lanes exist, and a
  cut waiting for approval. The first cut stage now reads "Starting the cut
  on GitHub".
- Each OBS instance card in the pre-release sync step lists every unit with
  its outcome there: synced, failed, or not on this instance (populate report
  outcome `skipped`). A card where every unit is skipped reads "Nothing to
  sync". A skipped unit is no longer counted as failed.
- A unit that every instance skipped was populated nowhere: the sync is shown
  as failed, with the reason, and QA cannot be approved.

- While a release is being cut, the Release tab names the units being cut
  (for example `ppg:staging:16:tde`) where it showed the internal timestamp id.

## [0.2.1] - 2026-11-11

### Added

- Loading indicators on the Release tab while the releases, the release units
  and the history are being fetched, and a message when the first load fails.

### Fixed

- Release tab listing already-finished merged release PRs as releases in progress.

## [0.2.0] - 2026-11-11

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
