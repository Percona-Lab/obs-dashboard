# Self-upgrade from the dashboard UI — Design

Date: 2026-11-06
Status: approved in brainstorming, pending written-spec review

## Goal

Let a signed-in dashboard user apply a newly published release of
obs-dashboard from the UI. The target host runs Linux with systemd and
Podman (Quadlet), behind a TLS-terminating reverse proxy. How that host is
provisioned is out of scope. This spec defines what the dashboard ships and
the contract any deployment must honour.

## Decisions (made with the user)

- **Delivery model:** approval-gated upgrades. CI publishes a pinned image
  per release. The dashboard only *requests* an upgrade, and a host-side script
  applies it. The container gets no container-runtime socket.
- The repo is `Percona-Lab/obs-dashboard`, and it is public. No GitHub token
  is needed anywhere.
- **Host layout:** `/etc/obs-dashboard` and `/var/lib/obs-dashboard`, with a
  root apply script triggered by a systemd path unit.
- **Runtime:** Podman Quadlet. Docker is not required.
- **Site-specific files** belong to whoever deploys the host: `env`,
  `config.yaml`, the reverse proxy and the TLS certificate.
- The app-owned host files are **kept and tested in this repo**
  (`deploy/host/`): the Quadlet unit `obs-dashboard.container`, `apply.sh`
  and the `obs-dashboard-apply.path`/`.service` units. They are attached to
  every GitHub release as a checksummed tarball, and the deployment downloads
  and installs them for a pinned version.
- **Host changes are announced in the changelog.** A release that needs new
  host files has a `### Host changes` subsection. The dashboard reads it
  from the release notes. Until the host files are at least that version,
  the dashboard shows a prominent banner with the instructions, and it
  refuses to apply that release.
- A release therefore consists of the image plus that host-files tarball.
  Neither is applied to the host automatically: the UI upgrade changes only
  the image.
- Rollback is automatic only. It happens when the new version fails its
  health check, and it restores the previous pin and the pre-upgrade DB
  backup. The UI never offers downgrades; going back to an older working
  version is a manual operator task.
- There is no drain step. Background work consists of pollers and short OBS
  calls, all of which resume after a restart.
- Any signed-in (allowlisted) user may check for and apply upgrades. This is
  the same access level as Rebuild.
- **Ownership:** this spec owns the dashboard side, the host scripts
  (section 5) and the **host contract** (section 4). Provisioning a
  particular host is out of scope.

## 1. Releases and versioning

- **Version stamp.** A new package, `backend/internal/version`, declares
  `var Version = "dev"`. The `Dockerfile` adds `ARG VERSION=dev` and builds
  with `-ldflags "-X <module>/internal/version.Version=${VERSION}"`. The
  version is logged at startup.
- **Version scheme.** Tags are plain `vX.Y.Z` with no prerelease suffixes.
  The first release is `v0.1.0`.
- **`CHANGELOG.md`** uses the Keep a Changelog format and has an `Unreleased`
  section.
  - A version section may contain a **`### Host changes`** subsection. It is
    required whenever anything under `deploy/host/` changed since the
    previous tag.
  - Its body is free-form markdown aimed at the operator. It describes the
    change in terms of the host itself: which file changed, and any new
    variable to add to `/etc/obs-dashboard/env` or setting to add to
    `config.yaml`, with an example value.
  - It never names a particular deployment tool.
  - The generic manual steps (download, verify, install, set the version,
    reload) are added by the UI. The subsection only needs the
    release-specific extras.
  - The `verify` job fails if `deploy/host/` (excluding `test/`) changed
    since the previous `v*` tag and the tag's section has no
    `### Host changes`.
- **`scripts/release.sh X.Y.Z`** is exposed as `task release VERSION=X.Y.Z`.
  It does the following:
  - validates the version;
  - moves `Unreleased` into `## [X.Y.Z] - YYYY-MM-DD`;
  - bumps `frontend/package.json`;
  - commits with `git commit -s`;
  - creates the annotated tag `vX.Y.Z`.

  It never pushes; the operator pushes the tag.
- **`.github/workflows/release.yml`** triggers on tag `v*` and runs three
  jobs:
  1. `verify` runs Go tests, the frontend tests and build, and checks that
     `CHANGELOG.md` has a section for the tag.
  2. `image` builds `linux/amd64` with `VERSION=<tag>` and pushes
     `ghcr.io/percona-lab/obs-dashboard:<tag>` using `GITHUB_TOKEN`
     (`packages: write`). It never pushes a `latest` tag.
  3. `release` builds `obs-dashboard-host-<tag>.tar.gz` (the contents of
     `deploy/host/`, minus `test/`) and a `SHA256SUMS` file covering it. It
     then runs `gh release create <tag>` with both attached, using the tag's
     changelog section as the notes.
- The `verify` job also runs the host-script tests (section 5).
- **One-time steps:** make the ghcr package public after the first publish.
  This needs a package or org admin; the user currently has push but not
  admin rights on the repo. Also check that org policy allows Actions to
  publish packages.
- **Unchanged:** `ci.yml` and the dev compose files.
  `docker-compose.prod.yml` stays for compose-based deployments, without UI
  upgrades.

## 2. Backend: `internal/upgrade`

### Release source

- `GET https://api.github.com/repos/{repo}/releases?per_page=30`, anonymous,
  with a 15s timeout.
- Drafts, prereleases, releases without `published_at`, and tags that do not
  match `^v\d+\.\d+\.\d+$` are skipped.
- Each kept release provides `version`, `notes` (the release body), `url`
  (`html_url`) and `published_at`.
- It also provides `host_changes`. This is the text of the
  `### Host changes` subsection in the body: everything after that heading
  (case-insensitive, at any heading level of 2–4 `#`) up to the next heading
  of the same or a higher level, trimmed. It is empty when the subsection is
  absent.
  - The release body is the tag's changelog section, so this is how the
    dashboard parses the CHANGELOG. It does not fetch `CHANGELOG.md`
    separately.
- A small `semver` helper parses and compares plain `vX.Y.Z` versions.

### Config

- `upgrades.github_repo` defaults to `Percona-Lab/obs-dashboard`.
- `upgrades.check_interval` defaults to `1h`, with a minimum of `5m`.
- Both settings are optional.
- `UPGRADE_APPLY_DIR` is an env var. When it is unset, applying is disabled.
- `OBS_DASHBOARD_HOST_FILES_VERSION` is an env var that the deployment
  sets to the host-files version it installed (`vX.Y.Z`).
  - If it is unset or invalid while applying is enabled, the dashboard
    treats the host-files version as unknown. Every release that has
    `host_changes` is then blocked, and the banner tells the operator to
    set it.

### Storage

There is a new table:

```
upgrades(
  version       TEXT PRIMARY KEY,
  notes         TEXT NOT NULL,
  url           TEXT NOT NULL,
  host_changes  TEXT NOT NULL DEFAULT '',
  published_at  TEXT NOT NULL,
  status        TEXT NOT NULL,   -- available|applying|applied|failed|superseded
  requested_by  TEXT,
  requested_at  TEXT,
  finished_at   TEXT,
  error         TEXT
)
```

Timestamps are bound as RFC3339Nano strings, following the existing store
convention.

### Manager

- **`Check(ctx)`**:
  - Fetches releases.
  - Upserts every release newer than `version.Version` as `available`.
  - Never downgrades the status of an `applying`, `applied` or `failed`
    row; only its notes and url are refreshed.
  - Marks `available` and `failed` rows that are no longer newer than the
    running version as `superseded`.
  - When `version.Version == "dev"`, it lists releases but treats none as
    applicable.
- **Host requirement of a target `T`.** Consider every known release `R`
  with `current < R <= T` and a non-empty `host_changes`.
  - `requires_host` is the highest such `R`, or empty when there is none.
  - `host_instructions` is the list of `{version, host_changes}` for all
    such `R`, oldest first. Skipping intermediate releases therefore still
    surfaces their host changes.
  - `T` is **blocked** when `requires_host` is set and the host-files
    version is unknown or lower than `requires_host`.
- **`Run(ctx)`** calls `Check` at startup and then every `check_interval`.
  Errors are logged and the previous state is kept.
- **`Apply(ctx, version, actor)`**:
  1. Fails with `ErrDisabled` when applying is disabled or the running
     version is `dev`.
  2. Fails with `ErrConflict` when any row is `applying`.
  3. Requires the row to be `available` and newer than the running version.
     It returns `ErrNotFound` for an unknown version and `ErrConflict`
     otherwise.
  4. Returns `ErrHostUpdateRequired` when the target is blocked.
  5. Backs up the DB with `VACUUM INTO '<dbdir>/backups/pre-<version>-<UTCts>.db'`,
     using the timestamp format `20060102T150405Z`. After a successful
     backup it deletes all but the newest 5 `pre-*.db` files. If the backup
     fails, it returns an error and writes nothing.
  6. Writes the apply request atomically (section 4).
  7. Sets the row to `applying`, with `requested_by` and `requested_at`
     filled in.

  The whole sequence runs under a mutex.
- **`Reconcile()`** runs at startup and every 15s while applying is enabled:
  - If `apply-status` exists, it parses the file. It sets the matching
    `version` row to `applied` (`ok=true`) or `failed`, with the `error`
    text and `finished_at`, and then deletes the file.
  - At startup only: if a row is `applying` and neither `apply-request` nor
    `apply-status` exists, the row becomes `failed` with error
    `"interrupted"`.
  - An `applied` row whose version differs from the running version is
    left as is. The UI shows the running version from `version.Version`.

### Health

`GET /healthz` is public and outside `/api`. It returns
`{"status":"ok","version":"<version.Version>"}` with status 200 once the HTTP
server is serving. No dependency checks are included; startup already fails
hard on DB or config errors.

## 3. API and UI

### API

All of these routes sit in the existing signed-in group (`RequireUser`). The
POSTs also use `CheckOrigin`, which is the same pattern as
`/api/settings/users`.

- `GET /api/upgrades` returns
  `{current, host_files_version, apply_enabled, in_flight, releases:[{version, notes, url, published_at, status, requested_by, requested_at, finished_at, error, host_changes, requires_host, blocked, host_instructions}]}`.
  - `host_files_version` is an empty string when unknown.
  The list is ordered newest first. Superseded rows are omitted, except for
  the last 10 `applied` and `failed` rows, which form the history.
- `POST /api/upgrades/check` returns 200 with the same body as the GET, or
  502 if GitHub fails.
- `POST /api/upgrades/{version}/apply` returns:
  - 202 with the release row on success;
  - 404 for an unknown version;
  - 409 when the version is not applicable or another upgrade is in flight;
  - 409 with `{"error":"host_update_required","requires_host":"vX.Y.Z"}`
    when the target is blocked;
  - 503 when applying is disabled;
  - 500 when the backup or the request write fails.

### UI

- **Service zone.** The Settings page gets a "Service" zone above "Access":
  - It shows the current version, a "Check now" button and the time of the
    last check.
  - It lists available releases. Each shows its version, published date and
    a link to GitHub. The notes are rendered as `white-space: pre-wrap` text,
    so there is no markdown dependency.
  - Each release has an "Upgrade" button with an inline confirm step. The
    button is disabled, with an explanation, when `apply_enabled=false` or
    when an upgrade is in flight.
  - A history list shows applied and failed rows, with the error text for
    failures.
- **While an upgrade is applying:**
  - The page polls `GET /api/upgrades` every 5s.
  - While requests fail with network errors or 502/503 from nginx, it shows
    "Restarting…".
  - Once the reported `current` equals the target version, it shows
    "Upgraded to vX.Y.Z" with a "Reload" button, because the open page is
    still running the old frontend.
  - If the row ends `failed`, it shows the error. The rollback has already
    happened by then.
- **Update dot.** A signed-in user sees a small dot on the header's Settings
  pill when any release is `available`. This comes from one
  `GET /api/upgrades` after sign-in, refreshed every 15 minutes.
- **Host-update banner.** The banner is shown when the newest `available`
  release is `blocked`.
  - For signed-in users it is a full-width, high-contrast (amber) banner
    under the header on every tab. It is not dismissible while the
    condition holds.
  - It reads: "vX.Y.Z needs a host update before it can be installed from
    the UI."
  - It shows the `host_instructions` text for each release, rendered as
    pre-wrapped text and labelled with its version.
  - It ends with fixed, tool-agnostic manual steps, run as root on the host.
    `<v>` is `requires_host`, and the release URL is built from
    `upgrades.github_repo`:
    ```
    cd "$(mktemp -d)"
    curl -fLO https://github.com/<repo>/releases/download/<v>/obs-dashboard-host-<v>.tar.gz
    curl -fLO https://github.com/<repo>/releases/download/<v>/SHA256SUMS
    sha256sum -c --ignore-missing SHA256SUMS
    tar xzf obs-dashboard-host-<v>.tar.gz
    install -m 0644 obs-dashboard.container /etc/containers/systemd/
    install -D -m 0755 apply.sh /usr/local/lib/obs-dashboard/apply.sh
    install -m 0644 obs-dashboard-apply.path obs-dashboard-apply.service /etc/systemd/system/
    # apply the release-specific host changes listed above, then:
    sed -i 's/^OBS_DASHBOARD_HOST_FILES_VERSION=.*/OBS_DASHBOARD_HOST_FILES_VERSION=<v>/' /etc/obs-dashboard/env
    #   (append the line instead if it is not present)
    systemctl daemon-reload
    systemctl enable --now obs-dashboard-apply.path
    systemctl restart obs-dashboard
    ```
  - Below the steps: "If your host is managed by a deployment tool, make
    the equivalent change there instead, so it is not reverted on the next
    run. The dashboard restarts with the new host files, and this banner
    clears."
  - The same steps are documented in `deploy/host/README.md`. The banner
    links to that file at the `requires_host` tag.
  - Also shown: the current host-files version, or "unknown — set
    `OBS_DASHBOARD_HOST_FILES_VERSION`".
  - On the Settings Service zone, the same instructions appear inline on
    each blocked release, and its Upgrade button is disabled with "Host
    update required".
  - Anonymous users never see the banner.

## 4. Host contract

Any deployment of the dashboard with UI upgrades must provide the following,
and the dashboard relies on nothing else. A manual install following
`deploy/host/README.md` is one valid implementation.

- **Image:** `ghcr.io/percona-lab/obs-dashboard:vX.Y.Z`. It is public,
  `linux/amd64`, runs as root inside the container, and listens on `:4000`.
  The entrypoint is `/obsboard`. `trivy` and `wget` are present in the image.
- **Container mounts:**
  - `/data` is persistent and read-write. It holds `obsboard.db`, which is
    `store.db_path`, and `/data/backups/`, which the dashboard creates.
  - `/apply` is read-write and is the apply handshake directory. Host side:
    `/etc/obs-dashboard/apply`.
  - The config file is read-only, at the path given by `CONFIG_FILE`.
- **Env:**
  - `CONFIG_FILE` and `UPGRADE_APPLY_DIR=/apply`, which are set in the
    shipped unit;
  - the rest come from `/etc/obs-dashboard/env`, maintained by the operator
    or their deployment tool:
  - `AUTH_PUBLIC_URL`;
  - `OBS_DASHBOARD_HOST_FILES_VERSION`, which must equal the installed
    host-files version;
  - `GOOGLE_CLIENT_ID` and `GOOGLE_CLIENT_SECRET`;
  - `OBS_DASHBOARD_ADMINS`;
  - the per-instance `<ENV>_USERNAME`, `_PASSWORD`, `_MQ_USERNAME` and
    `_MQ_PASSWORD` credentials.
- **Health:** `GET http://127.0.0.1:4000/healthz` returns 200 with
  `{"status":"ok","version":"vX.Y.Z"}`. An upgrade succeeds only when the
  reported version equals the requested one.
- **`apply-request`** is written by the dashboard. It is an atomic write: a
  temp file in the same directory, `fsync`, then `rename`. Mode is 0644.

  ```json
  {"version":"v0.2.0","previous":"v0.1.0","backup":"pre-v0.2.0-20261106T120000Z.db",
   "requested_by":"someone@percona.com","requested_at":"2026-11-06T12:00:00Z"}
  ```

  - `backup` is a bare file name inside `/data/backups/`, which is
    `/var/lib/obs-dashboard/backups/` on the host.
  - The host must treat every field as untrusted. It validates `version`
    against `^v\d+\.\d+\.\d+$` and requires `backup` to be a bare name
    matching `^pre-v\d+\.\d+\.\d+-\d{8}T\d{6}Z\.db$` that exists. The image
    repository never comes from the request.
- **`apply-status`** is written by the host when it finishes. It is an
  atomic write, and the host then removes `apply-request`.

  ```json
  {"version":"v0.2.0","previous":"v0.1.0","ok":false,
   "error":"health check timed out after 120s; rolled back to v0.1.0",
   "finished_at":"2026-11-06T12:03:10Z"}
  ```

  The dashboard consumes and deletes it, either at startup or within 15s.
- **Host behaviour the dashboard expects:**
  - The current version keeps running if the pull fails.
  - On a failed health check, the host restores the previous image pin and
    restores `obsboard.db` from `backup` (removing the `-wal` and `-shm`
    files) with the service stopped, then starts the old version.
  - Exactly one `apply-status` is written per request.
- **Reverse proxy:** `https://dashboard.obs.pg.labs.percona.com` proxies to
  `127.0.0.1:4000`. Buffering is off and read timeouts are long, so SSE on
  `/api/stream` works. `/auth/*`, `/api/*` and static paths are passed
  through unchanged.
- **Files the deployment must provide alongside the shipped host files:**
  - a version drop-in at
    `/etc/containers/systemd/obs-dashboard.container.d/10-version.conf`
    containing exactly one `Image=ghcr.io/percona-lab/obs-dashboard:vX.Y.Z`
    line inside `[Container]`. It is written once at install, and afterwards
    only by `apply.sh`. A deployment tool must not overwrite it when it
    already exists;
  - the host directories `/etc/obs-dashboard/apply` (mounted at `/apply`)
    and `/var/lib/obs-dashboard` (mounted at `/data`).
- **Host-files tarball:** the deployment fetches
  `https://github.com/Percona-Lab/obs-dashboard/releases/download/<v>/obs-dashboard-host-<v>.tar.gz`
  and `SHA256SUMS` for a pinned `<v>`, verifies the checksum and installs the
  files:
  - `obs-dashboard.container` → `/etc/containers/systemd/`;
  - `apply.sh` → `/usr/local/lib/obs-dashboard/apply.sh` (0755 root);
  - the `.path` and `.service` units → `/etc/systemd/system/`.

  It then runs `daemon-reload` and enables `obs-dashboard-apply.path`. It
  restarts `obs-dashboard` when the unit or `env` changed. It also writes
  the installed version into `env` as `OBS_DASHBOARD_HOST_FILES_VERSION`.
- **Compatibility.** The host-files version is pinned independently of the
  running app version, so changes must be additive:
  - The `apply-request`/`apply-status` formats may only gain fields, never
    lose or redefine one.
  - Unit changes may only add mounts or env vars.
  - Any change under `deploy/host/` must be announced in that release's
    `### Host changes`.
  - The app fails fast at startup when a mount or env var it requires is
    missing. The health check then fails, and the upgrade rolls back
    instead of half-working.

## 5. Host scripts (`deploy/host/`)

- **`README.md`** is the tool-agnostic manual install and update procedure:
  - prerequisites: Podman with Quadlet, `jq`, `curl`, and a reverse proxy;
  - the directory layout;
  - the `env` variables;
  - writing the initial version drop-in;
  - the install and update commands shown in the banner;
  - how to roll back manually to an earlier version (re-pin and restore a
    backup).

  It is included in the tarball.

- **`obs-dashboard.container`** is a static Quadlet unit with no templating.
  It produces `obs-dashboard.service`.

  ```ini
  [Unit]
  Description=OBS Dashboard
  Wants=network-online.target
  After=network-online.target

  [Container]
  ContainerName=obs-dashboard
  # Image= lives in obs-dashboard.container.d/10-version.conf (written once by
  # the installer, rewritten only by apply.sh).
  PublishPort=127.0.0.1:4000:4000
  Volume=/var/lib/obs-dashboard:/data:Z
  Volume=/etc/obs-dashboard/config.yaml:/config/config.yaml:ro,Z
  Volume=/etc/obs-dashboard/apply:/apply:Z
  EnvironmentFile=/etc/obs-dashboard/env
  Environment=CONFIG_FILE=/config/config.yaml
  Environment=UPGRADE_APPLY_DIR=/apply
  HealthCmd=wget -qO- http://127.0.0.1:4000/healthz
  HealthInterval=30s
  HealthStartPeriod=60s

  [Service]
  Restart=always
  TimeoutStartSec=300

  [Install]
  WantedBy=multi-user.target
  ```

- **`obs-dashboard-apply.path`** uses
  `PathExists=/etc/obs-dashboard/apply/apply-request` and starts
  `obs-dashboard-apply.service`. It is `WantedBy=paths.target`.
- **`obs-dashboard-apply.service`** is a oneshot, `User=root`, that runs
  `/usr/local/lib/obs-dashboard/apply.sh`. It sets `StartLimitIntervalSec=300`
  and `StartLimitBurst=3`.
- **`apply.sh`** is bash with `set -euo pipefail`. Every path and command is
  overridable through env vars, for the tests only: `APPLY_DIR`,
  `DATA_DIR`, `DROPIN`, `UNIT`, `IMAGE_REPO`, `HEALTH_URL`,
  `HEALTH_TIMEOUT`, `PODMAN`, `SYSTEMCTL`, `CURL`. The defaults are the
  production values from section 4, and `IMAGE_REPO` defaults to
  `ghcr.io/percona-lab/obs-dashboard`.
  1. Read and validate `apply-request` per section 4. JSON is parsed with
     `jq`, a host prerequisite. If validation
     fails, the script writes a failed status and removes the request.
  2. Read the previous pin from the drop-in, then
     `podman pull "$IMAGE_REPO:$version"`. If the pull fails, it writes a
     failed status. Nothing else has changed at that point.
  3. Copy the drop-in to `10-version.conf.prev` and write the new pin
     atomically. Then run `systemctl daemon-reload` and
     `systemctl restart obs-dashboard`.
  4. Poll `HEALTH_URL` every 2s until `.version == $version`, up to
     `HEALTH_TIMEOUT` (120s).
  5. **On failure** (step 3 or 4, or any error caught by the `ERR`/`EXIT`
     trap after step 3):
     - stop the service;
     - restore `obsboard.db` from `DATA_DIR/backups/$backup` and remove the
       `-wal` and `-shm` files;
     - restore the drop-in from `.prev`, reload and start the service.

     The status is `ok:false`, with the error and "rolled back to
     <previous>". If the rollback itself fails, the error says
     `rollback failed: …`.
  6. On success, run `podman image prune -f` (failures here are ignored)
     and write the status `ok:true`.
  7. Always write exactly one `apply-status` atomically, then remove
     `apply-request`. Log every step to stdout, which goes to the journal.
- **Tests** (`deploy/host/test/run.sh`, plain bash, no bats):
  - Each case runs `apply.sh` in a temp directory against stub
    `podman`, `systemctl` and `curl` scripts. The stubs record their calls
    and fail on demand.
  - It asserts the resulting drop-in, DB file, status JSON and call log.
  - Cases:
    - success;
    - invalid version;
    - backup name that is invalid or contains path traversal;
    - missing backup;
    - pull failure;
    - health timeout, then rollback;
    - health reporting the wrong version, then rollback;
    - a failed rollback restart.
  - `ci.yml` gets a `host-scripts` job that runs:
    - `shellcheck` on `apply.sh` and the test files;
    - `systemd-analyze verify` on the `.path` and `.service` units;
    - the Podman Quadlet generator in dry-run mode on
      `obs-dashboard.container` plus a sample `10-version.conf` drop-in;
    - the test runner. The `release.yml` verify job runs the same checks.

## 6. Failure handling

| Situation | Result |
|---|---|
| GitHub unreachable or rate-limited | Logged, and the list keeps its last state. "Check now" returns 502. Anonymous quota is 60 requests/h; hourly checks use 1. |
| Apply while one is in flight | 409 |
| `VACUUM INTO` fails | 500, no request is written, and the host is untouched |
| Pull fails, or the new version is unhealthy | The host rolls back, and the status arrives as `failed` with the reason |
| Dashboard restarted mid-upgrade | Reconcile consumes the status. If neither a request nor a status exists, the row becomes `failed` ("interrupted") |
| Malformed `apply-status` | Logged and left on disk for an operator. The row stays `applying`, and a new apply is blocked until it is resolved |

## 7. Testing

- **Host scripts:** see section 5. They run in `ci.yml` and in the release
  verify job.

- **Go:**
  - The release source against an `httptest` GitHub. Cases: drafts,
    prereleases, bad tags, ordering, and HTTP errors.
  - `host_changes` extraction: the subsection absent, at heading levels 2–4,
    followed by another heading, as the last section, and in mixed case.
  - The host requirement:
    - no host changes;
    - a skipped intermediate release with host changes;
    - host files equal to, lower than, or higher than the requirement;
    - an unknown host-files version.
  - Apply returns `ErrHostUpdateRequired` when blocked.
  - The semver helper.
  - Manager:
    - check, supersede, and no-downgrade of `applying` rows;
    - apply success, conflict, disabled, and `dev`;
    - backup creation and pruning to 5, with a real SQLite file;
    - the atomic request file contents;
    - reconcile for ok, failed, malformed and interrupted cases.
  - Handlers: 401 without a session, 403 with a bad Origin, and the
    202/404/409/503 cases.
  - `/healthz`.
- **Frontend (Vitest):**
  - Service zone rendering, the confirm step, apply, and the polling states
    ("Restarting…", upgraded plus reload, failed).
  - The disabled state.
  - The Settings pill dot.
  - The host-update banner:
    - shown only to signed-in users when the newest release is blocked;
    - lists the instructions for every release involved;
    - shows the unknown-version text;
    - hides once no release is blocked;
    - disables the Upgrade button on blocked releases.
- **Release:** the changelog check fails when `deploy/host/` changed without
  a `### Host changes` subsection. It is tested as a script with fixture
  repos.
- **Release:** the first real `v0.1.0` tag exercises `release.yml` end to
  end.
- **Manual end-to-end (user gate, once a host is deployed):**
  1. Install `v0.1.0` on the host.
  2. Publish `v0.1.1` and upgrade from the UI.
  3. Force a failure and confirm that `v0.1.1` remains running and the UI
     shows the failure. The simplest way to force one is to stage a request
     for a tag whose image does not exist.

## Non-goals

- Downgrades from the UI, and multi-arch images.
- Upgrading anything other than the dashboard container (nginx,
  certificates, OS).
- Scheduled or automatic upgrades without a human click.
- Showing release notes as rendered markdown.
