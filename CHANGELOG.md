# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/). Versions are plain
`vX.Y.Z` tags.

A release that changes anything under `deploy/host/` must include a
`### Host changes` subsection. It tells the operator what changed on the host
and any new `/etc/obs-dashboard/env` variable or `config.yaml` setting, with
an example value. See `deploy/host/README.md`.

## [Unreleased]

## [0.2.8] - 2026-11-13

### Added

- The QA step of a release has a "Skip QA" button. After a warning and a confirmation it makes "Release" available without QA, for one version of the release PR, and leaves a comment on the PR. "Update to latest main & restart" asks for QA again.

## [0.2.7] - 2026-11-12

### Added

- Settings has a "Server log" tab that shows the backend's log live, for signed-in users.
- The backend writes its log to `obs-dashboard.log` next to the database, as well as to standard error.

### Host changes

- No action needed. A log file appears at `/var/lib/obs-dashboard/obs-dashboard.log`. It is capped at 10 MB, with one previous file kept as `obs-dashboard.log.1`. The journal (`journalctl -u obs-dashboard`) still has everything, including the HTTP request log and panic stacks; the file and the Settings tab hold only the application's own log lines.

## [0.2.6] - 2026-11-12

### Added

- The Release tab approves the workflow runs GitHub holds on a release PR
  after a re-cut. The cut workflow pushes the release branch as
  `github-actions[bot]`, and GitHub then waits for a person to press "Approve
  workflows to run" on the PR. The dashboard now approves those runs itself,
  only for releases it cut and only for runs caused by that push. If GitHub
  refuses, the run stays held and is approved on the PR as before.

## [0.2.5] - 2026-11-12

### Fixed

- Release tab stuck on "Opening the release PR" for a release of several
  units. The PR description was built from every unit's changelog section and
  could exceed GitHub's limit of 65536 characters, so GitHub refused it. The
  description now lists the units being released and their release ids, and
  no longer carries the changelog. The title is bounded too.
- A release PR that cannot be opened is now shown in the wizard, with the
  reason and how to open it by hand, instead of a spinner that never ends.

## [0.2.4] - 2026-11-12

### Changed

- Every button of the Release tab shows a spinner from the click until the
  release has changed on screen. After a retry, where GitHub takes a few
  seconds to show the new run, the view refreshes every 3 seconds until it
  does, for at most 90 seconds.

## [0.2.3] - 2026-11-12

### Fixed

- Release tab waiting for QA forever when a release has no QA tests. A QA run
  that finishes without a single lane now counts as finished: the QA step
  says that no tests are defined and lets you release.

## [0.2.2] - 2026-11-12

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
