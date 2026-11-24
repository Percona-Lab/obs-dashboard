# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/). Versions are plain
`vX.Y.Z` tags.

A release that changes anything under `deploy/host/` must include a
`### Host changes` subsection. It tells the operator what changed on the host
and any new `/etc/obs-dashboard/env` variable or `config.yaml` setting, with
an example value. See `deploy/host/README.md`.

The dashboard blocks a release with a `### Host changes` subsection until
`OBS_DASHBOARD_HOST_FILES_VERSION` on the host is at least that release. Add
the subsection only when the host must change before the upgrade. An optional
new variable or setting belongs under `### Added` or `### Changed`.

## [Unreleased]

## [0.2.16] - 2026-11-24

### Changed

- The host update notice says which host files version it needs and what clears it: `OBS_DASHBOARD_HOST_FILES_VERSION` at that version or later, and a restart. It used to list only the release's host changes, which could say that nothing needed to change.
- A release that changes only `deploy/host/README.md` no longer needs a "Host changes" section, so it no longer blocks upgrades from the UI.

### Fixed

- A package recorded as "not a container" is checked again whenever a new build of it lands. A package first seen before its `Dockerfile` existed stayed a non-container for good: it got no image tags and no CVE scan, so its released image showed "No staging image to compare" in the CVEs tab. A package already stuck this way is corrected on its next build.

## [0.2.15] - 2026-11-23

### Added

- The Release tab can act on GitHub as a GitHub App instead of with a personal access token. Set `RELEASE_GITHUB_APP_ID` and `RELEASE_GITHUB_APP_PRIVATE_KEY`, and the dashboard requests short-lived installation tokens limited to the release repository. Pull requests, labels, comments and merges then appear as `<app-slug>[bot]`. The App must be installed on the repository with these permissions: `contents` write, `pull_requests` write, `actions` write, `checks` read. The installation is found from `release.github_repo`; no installation ID is configured.
- Settings → General has a "GitHub connection" card. It shows whether the Release tab uses the App or a token, whether the App is installed on the repository, the permissions it has been granted against the ones the Release tab needs, and when the current token expires. "Test connection" runs the check again. Nothing on the card is editable.

### Changed

- `RELEASE_GITHUB_TOKEN` is now the fallback. It is used only when neither App variable is set. When either App variable is set, the App is used, and a half-set pair or an unreadable key is not replaced by the token: the dashboard still starts, the Release tab reports that it could not refresh, and the "GitHub connection" card says which variable is wrong.

### Host changes

- No host file changed, but this section still blocks the upgrade from the UI until `OBS_DASHBOARD_HOST_FILES_VERSION` is `v0.2.15` or later: set it and restart `obs-dashboard.service`. To switch to the GitHub App, add two lines to `/etc/obs-dashboard/env` and restart `obs-dashboard.service`. Set both or neither.

  ```sh
  RELEASE_GITHUB_APP_ID=123456
  # output of: base64 -w0 app.private-key.pem
  RELEASE_GITHUB_APP_PRIVATE_KEY=LS0tLS1CRUdJTi...
  ```

  `RELEASE_GITHUB_TOKEN` can stay in the file while switching over and be removed once the card shows "Connected".

## [0.2.14] - 2026-11-19

### Fixed

- The dashboard stopped receiving build events from an OBS instance once its RabbitMQ broker was upgraded to a build with the fix for CVE-2026-57221, such as `rabbitmq-server-4.1.5-160000.3.1`. The log repeated `mq: disconnected, reconnecting` with `ACCESS_REFUSED - configure access to exchange` every 30 seconds, and the instance fell back to polling only. Those brokers require configure permission to check that an exchange exists, which a read-only consumer account does not have. The dashboard no longer makes that check. It binds its queue to the exchange directly, which needs only read permission, so no change to the broker account is needed. A missing exchange is still reported, by the bind.

## [0.2.13] - 2026-11-18

### Fixed

- Released container images showed the version and tags of the previous release, for example `14.24-1` after `14.25-1` was released, in the "Released" view of the CVEs tab and wherever else the image's tags are shown. The tags were read once and never again. They are now re-read when the release project publishes, once a day, and at startup. When the tags change, the image is rescanned straight away instead of at the next nightly scan.

## [0.2.12] - 2026-11-17

### Added

- The CVE scanner now runs Grype after Trivy on every image and stores one merged list of findings. Trivy reports no OS vulnerabilities on RHEL 10 images, so ubi10 images used to show as clean; Grype finds them. When both scanners report the same CVE for the same package, it is listed once.
- Each finding returned by the CVE endpoints has a `scanners` field listing which scanner reported it: `trivy`, `grype` or both. Findings stored before this version have no `scanners` field until the image is rescanned.

### Changed

- A scan now fails when either scanner fails, and the previous result is kept. Grype downloads its vulnerability database the first time it runs in a container, so the container needs outbound access to Grype's database host as well as Trivy's.
- Each scan takes about twice as long, because each scanner pulls the image itself.
- Existing images pick up Grype's findings at the next nightly rescan.

## [0.2.11] - 2026-11-17

### Added

- A "CVEs" tab after Artifacts has three views. "Staging" lists the packages to bump to clear the HIGH and CRITICAL CVEs found in the staging images, split into "Ours to bump", "Base OS" and "Not classified yet". Entries that are new since you last looked are marked, and "Mark all as seen" clears the markers; the markers are kept in your browser, and the tab shows a badge when there are new entries. "Released" shows, for each released image, how many of its CVEs the current staging image already fixes. "By image" is the CVE table that used to be on Overview.
- `GET /api/cve/report` returns the data behind the CVEs tab: the staging work list and the released-image view. It is public, like the other CVE endpoints.

### Changed

- The CVE table moved from Overview to the "By image" view of the new CVEs tab.
- After upgrading, images scanned before this version are rescanned once at startup so their findings can be classified. Until the rescan finishes, their packages appear under "Not classified yet". Entries marked as seen before that rescan finishes may show as new once more afterwards, because the rescan adds the file a library was found in.

## [0.2.10] - 2026-11-17

### Added

- `GET /api/cve/findings` lists every image scan that currently has HIGH or CRITICAL CVEs, across all images, with the findings. An optional `?project=` prefix such as `ppg:staging` keeps that project and its subprojects.

## [0.2.9] - 2026-11-13

### Added

- Once QA has finished, each failed lane in the QA step has a "Re-run" button, and the step has a "Re-run all failed" button. Both ask for confirmation, then run the lanes again on the same QA run; passed lanes keep their result.

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
