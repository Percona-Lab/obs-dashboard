# PPG OBS Dashboard

A real-time build monitoring dashboard for PPG packages on the Open Build Service.
The dashboard polls OBS on a schedule and consumes RabbitMQ build events, stores state in SQLite, and renders a live board view with per-package rollup states and a build event log.

## What you get

- Package cards showing live rollup state (building, failed, succeeded, published) across all PPG OBS projects.
- Per-target build event log: build started, succeeded, failed, published — filterable by tag and time window.
- Artifacts view — binary package download links and container image pull commands, browsable by version, repo, and arch; covers dev builds, releases, and PRs.
- Tag-based filtering (RPM, DEB, container) and version selector.
- PR build tracking: dedicated board context per OBS PR branch.
- SSE-based live updates — cards and event log update in real time without a page refresh.

## Quick start

```sh
git clone <repo-url> obs-dashboard
cd obs-dashboard

cp .env.example .env     # add OBS credentials (required)

task dev                 # http://localhost:4000
```

`task dev` builds and starts both the Go backend and the Vite dev server in Docker.
The frontend proxies API calls to the backend, so only `:4000` needs to be open.

## Configuration

Secrets and runtime settings live in `.env`. An optional `config.yaml` provides the
same knobs in YAML form — environment variables always take precedence.

### `.env`

| Variable | Default | Purpose |
|---|---|---|
| `OBS_USERNAME` | *(required)* | OBS account username |
| `OBS_PASSWORD` | *(required)* | OBS account password |
| `OBS_BASE_URL` | `https://api.opensuse.org` | OBS API base URL |
| `MQ_URL` | `amqps://opensuse:opensuse@rabbit.opensuse.org:5671/` | RabbitMQ connection URL |
| `POLL_INTERVAL` | `2m` | How often the poller fetches OBS build results |
| `DB_PATH` | `/data/obsboard.db` | SQLite database path (inside the container) |
| `EVENT_RETENTION` | `7d` | How long build events are kept |
| `HTTP_PORT` | `4000` | Port the backend listens on |

Copy `.env.example` for a pre-filled template.

### `config.yaml` (optional)

`config.yaml.example` shows the equivalent YAML structure. Useful if you prefer
file-based config or need to set fields not exposed as env vars (e.g. `obs_root`).
The file is read on startup; environment variables override any value it contains.

## Authentication

Anyone can view the dashboard. Actions that change state (Rebuild, the
telemetry toggle) require signing in with an allowlisted `@percona.com`
Google account.

1. In Google Cloud Console → APIs & Services → Credentials, create an
   OAuth client of type **Web application** with these authorized redirect
   URIs:
   - `https://dashboard.obs.pg.labs.percona.com/auth/callback`
   - `http://localhost:5173/auth/callback` (local dev)
2. Set in `.env` (single-quote values that contain `$`):
   - `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`
   - `OBS_DASHBOARD_ADMINS` — comma-separated emails that are always allowed
3. Set `auth.public_url` in `config.yaml` (or `AUTH_PUBLIC_URL`) to the URL
   the dashboard is served from, as scheme and host only (no path). It drives
   both the OAuth redirect (`<public_url>/auth/callback`) and the Origin
   check on actions, so sign-in only works from that exact origin.
4. **Local development:** set `AUTH_PUBLIC_URL=http://localhost:5173` in
   `.env`. The dev UI is served by Vite on 5173 (the backend on 4000 serves
   only `/api`); leaving the production value from the example config would
   send the OAuth redirect to the production callback and make every action
   fail with 403.

Once `GOOGLE_CLIENT_ID` is set, `GOOGLE_CLIENT_SECRET` and `public_url` are
required or startup fails. Sessions last `auth.session_ttl` (env
`AUTH_SESSION_TTL`, default `168h`).

Admins and anyone they add can manage the allowlist on the **Settings** tab.
Removing someone ends their sessions immediately.

Without `GOOGLE_CLIENT_ID` the dashboard runs read-only: there is no Sign in
button and action endpoints return 401.

## Release tab

The Release tab cuts PPG releases from the dashboard. A signed-in user selects
release units, approves the generated changelogs, watches the pre-release sync
and QA, and approves the final release. Each step is an action on the
`percona/obs-packaging` GitHub repository; the dashboard holds no release state
of its own. The design is in
[docs/superpowers/specs/2026-11-11-release-tab-design.md](docs/superpowers/specs/2026-11-11-release-tab-design.md).

To enable it:

- `RELEASE_GITHUB_TOKEN` (or `release.github_token`): a classic personal access
  token with the `repo` and `workflow` scopes. All GitHub actions appear as the
  token's owner.
- `RELEASE_GITHUB_REPO` (or `release.github_repo`): the repository to act on.
  Default `percona/obs-packaging`.

Without a token the feature is off: the tab is hidden and the release API
returns 404.

The tab requires the obs-packaging changes listed in section 6 of the design
(cut workflow, sync gate, Jenkins annotation, run names, unit list file,
deletion report). It does not work against a repository that lacks them.

Who performed each step is recorded only in the dashboard's own audit log. It
is never written to GitHub, because the repository is public.

## Development

```sh
task dev          # start backend (:4000) + Vite dev server (:5173), hot reload
task down         # stop the development stack
```

The working tree is bind-mounted into the frontend container, so edits appear
live. Backend changes require a container rebuild (`task down && task dev`).

## Production

```sh
task redeploy     # (re)build and (re)start the production stack
task prod-logs    # tail production logs
task down-prod    # stop the production stack
```

The production build compiles the frontend into static assets and embeds them
in a single Go binary inside a minimal Alpine image. Persistent state lives in
`./data` (bind-mounted into the container).

### Upgrading to multi-instance

The first start of a multi-instance build migrates the SQLite database in
place: it strips the `<obs_root>:` prefix from every stored project name
(`isv:percona:ppg:17` → `ppg:17`) and stamps the legacy instance on stored
build targets and target events. The migration runs once and cannot be undone
by an older binary, which would re-insert root-prefixed rows next to the
migrated ones.

- **Before rolling out**, stop the stack and back up the database
  (`data/obsboard.db`, i.e. `DB_PATH` on the host).
- **To roll back**, stop the stack, restore that backup, and start the older
  image.
