# Release tab: authenticate as a GitHub App

Date: 2026-11-23. Status: design, not implemented.

Decisions marked **(user)** were made by the user. Decisions marked
**(my call)** were made while writing this document and have not been
individually confirmed.

## 1. Intent

The Release tab acts on `percona/obs-packaging` with a classic personal
access token from one person's account (`RELEASE_GITHUB_TOKEN`). That token
reaches every repository its owner can access, and every release action on
GitHub appears as that person. A GitHub App is now installed on
obs-packaging **(user)**. The dashboard is to act as that App instead, and
Settings is to show whether the App connection works **(user)**.

The release-tab design (`2026-11-11-release-tab-design.md` § 5.1) already
planned this replacement. Nothing about how a release maps onto GitHub
changes.

## 2. Goals, non-goals, anti-goals

Goals:

1. Every GitHub call the Release tab makes is authenticated with an
   installation token of the App, when App credentials are configured.
2. A card in Settings shows the state of the GitHub connection and lets a
   signed-in user re-test it, so "why is the Release tab broken" is answered
   without reading logs.
3. obs-sync (the deployment tool, outside this repository) can deploy the
   App credentials by writing two lines to the existing env file.

Non-goals:

- Editing any GitHub setting from the dashboard. The card is read-only
  **(user)**.
- Storing the App's private key, App ID or repository in the database
  **(user)**.
- Removing the personal-token path. It stays as a fallback until the App is
  confirmed in production; removal is a later change **(my call)**.
- Reconfiguring credentials while the dashboard runs. Credentials are read
  at startup, as today **(my call)**.
- Roles. Every signed-in user can see the card, like the rest of Settings.
- GitHub webhooks, or acting as a user through the App (user-to-server
  tokens).

Anti-goals:

- The private key, the App JWT or an installation token reaching the
  browser, a log line, an error message or an API response.
- A bad or missing key preventing the dashboard from starting.
- A second place where release credentials can be set, disagreeing with the
  environment.

## 3. Constraints

- Secrets reach the container only through `/etc/obs-dashboard/env` (0600,
  `EnvironmentFile=` in `deploy/host/obs-dashboard.container`). Podman env
  files hold one value per line and no multi-line values.
- Changing the container unit means a host-files version bump and a manual
  host update on every install. This change must not need one.
- Installation tokens expire one hour after they are issued.
- The App JWT must be RS256, may live at most 10 minutes, and its `iss` is
  the App ID or client ID.
- `go-jose/v4` is already a direct dependency (sign-in). No new dependency
  is to be added.
- The acting dashboard user's identity still never goes to GitHub; the audit
  log stays the only record of who did what.

## 4. Approaches

- **A. Token source inside the release client (chosen, user).**
  `release.Client` asks a small token source for a bearer token before each
  request. The App source signs a JWT, exchanges it for an installation
  token and reuses it until shortly before expiry; the personal-token source
  returns a constant. No new dependency, and the same component supplies the
  Settings card, because the installation lookup already returns the granted
  permissions.
- **B. `ghinstallation` as an HTTP transport.** The standard library for
  this, but it brings in `go-github` and a second JWT library for roughly 80
  lines of our own code, and hides the installation details the card needs.
- **C. A background loop that mints tokens and writes them into the
  client.** Leaves the client untouched but adds a goroutine and a window
  where a failed refresh leaves a dead token with no caller to report to.

For the key's delivery, a mounted key file was considered and rejected: it
needs a new `Volume=` line in the container unit (see § 3). The key travels
base64-encoded on one env line instead **(user)**.

## 5. Configuration (the contract with obs-sync)

Two new values, environment only in practice, bound through viper like the
existing release keys:

| Env | Config key | Value |
|---|---|---|
| `RELEASE_GITHUB_APP_ID` | `release.github_app_id` | The App ID (or client ID). Not secret. Treated as an opaque string and used as the JWT `iss`. |
| `RELEASE_GITHUB_APP_PRIVATE_KEY` | `release.github_app_private_key` | The App's private key PEM, base64-encoded (standard encoding) on one line: `base64 -w0 app.private-key.pem`. Secret. |

`RELEASE_GITHUB_REPO` (`release.github_repo`, default
`percona/obs-packaging`) is unchanged. The installation ID is not
configured: it is looked up from the repository (§ 6.2) **(my call)**.

obs-sync appends the two lines to `/etc/obs-dashboard/env` and restarts
`obs-dashboard.service`. Rotating the key is the same operation.

Which credentials are used **(my call)**:

| App ID | App key | `RELEASE_GITHUB_TOKEN` | Mode |
|---|---|---|---|
| set | set | any | `app` |
| one of the two set | | any | `app`, misconfigured (see below) |
| unset | unset | set | `token` (personal token, as today) |
| unset | unset | unset | `off` (tab hidden, routes 404, as today) |

App credentials win over the token when both are present, so the token can
stay in the env file during the switch-over and be deleted afterwards.

A half-set pair, a value that is not base64, or a key that is not a
parseable RSA private key (PKCS#1 or PKCS#8 PEM) does not fail startup. The
feature is enabled in mode `app` with a token source that returns a fixed
configuration error on every call; the Release tab shows its existing "could
not refresh" state and the Settings card says what is wrong. The dashboard
does not fall back to the personal token in that case: a silent fallback
would hide the misconfiguration. Startup logs one line with the mode and the
repository, and a warning for a configuration error. No log line contains
any part of either value.

## 6. Backend

### 6.1 Token source

`release.Client.Token string` becomes a token source: something that
returns a bearer token for a request context, or an error. `Client.do` calls
it once per request. Two implementations: a constant (personal token) and
the App source. Naming and exact shape are the builder's.

The App source:

1. Signs a JWT with the private key: RS256, `iss` = the App ID, `iat` 60
   seconds in the past (clock drift), `exp` 9 minutes ahead.
2. On first use, finds the installation:
   `GET /repos/{owner}/{repo}/installation` with the JWT. The installation
   ID is kept for the life of the process.
3. Mints a token: `POST /app/installations/{id}/access_tokens` with the JWT,
   body `{"repositories": ["<repo name>"]}` so the token is scoped to the
   one repository even if the App is installed on more **(my call)**.
4. Caches the token and returns it until 5 minutes before `expires_at`, then
   mints a new one. Concurrent callers share one mint (a mutex is enough).
5. When an API call made with an App token answers 401, the cached token is
   dropped so the next request mints a fresh one. The failed request is not
   retried **(my call)**.
6. A 404 on the installation lookup means "the App is not installed on this
   repository"; the installation ID is not cached.
7. A failed lookup or mint is remembered for 30 seconds and returned to
   callers without another GitHub call, so one Release-tab refresh (on the
   order of a hundred requests) does not repeat the failing call each time.
   The connection check (§ 6.2) ignores this memory **(my call)**.

The ETag cache in `Client` is left as it is: it is not cleared when a token
is dropped or replaced **(my call)**.

Errors from steps 2 and 3 are `*APIError` values (method, path, status), the
same sanitised form as every other GitHub error, so they pass through the
existing error handling unchanged. Configuration errors (§ 5) are fixed
strings that name the env variable and the kind of fault, never the value.

The App source also remembers, for the Settings card: when the current token
was minted, when it expires, and the last mint error with its time.

The JWT calls go through the same HTTP client and API base as the rest of
`release.Client` so tests can point everything at the fake GitHub
(`fake_github_test.go`), which gains the two App endpoints and `GET /app`.

### 6.2 Connection check

A check gathers the card's content. It always talks to GitHub; it is run
only when the card is opened or "Test connection" is pressed.

Mode `app`:

1. `GET /app` with the JWT: proves the key and App ID; gives the App's name
   and slug.
2. `GET /repos/{owner}/{repo}/installation` with the JWT: proves the App is
   installed on the repository; gives the installation ID and the granted
   permissions.
3. Obtains a token from the token source (minting if none is cached): proves
   an installation token can be issued.
4. Compares the granted permissions with the required set below.

The card's `app.id` is the ID GitHub returns from step 1, not the configured
value (which may be a client ID).

The check is not cached or throttled: it costs two or three GitHub calls,
only signed-in allowlisted users can reach it, and it runs only when the
card is opened or the button pressed **(my call)**.

The check stops at the first failing step and reports which step failed and
the sanitised error.

Mode `token`: `GET /repos/{owner}/{repo}` with the token; reports reachable
or the sanitised error. Mode `off`: no call.

Required permissions **(my call, derived from the calls in the `GitHub`
interface; to be confirmed against the real App, see § 9)**, kept as one
constant list:

| Permission | Level | Used for |
|---|---|---|
| `contents` | write | read files, list/delete `release/*` branches, amend commits, merge, releases and tags |
| `pull_requests` | write | open, close, list PRs; labels and comments on PRs |
| `actions` | write | dispatch workflows, re-run and approve runs, read runs, jobs and artifacts |
| `checks` | read | job annotations |
| `metadata` | read | always granted |

A permission is satisfied when the granted level is at least the required
one (`write` satisfies `read`). Missing permissions are a warning on the
card, not a failure: the tab stays enabled, and the individual action that
needs the permission fails with GitHub's own status as today.

### 6.3 Route

`GET /api/release/github`, in the existing signed-in group next to
`/api/release/status`, registered whether or not the feature is on
(mode `off` answers too). It runs the check and returns:

```json
{
  "mode": "app",
  "repo": "percona/obs-packaging",
  "ok": true,
  "error": "",
  "failed_step": "",
  "app": {"id": "123456", "slug": "percona-obs-release", "name": "Percona OBS Release"},
  "installation_id": 7654321,
  "permissions": [
    {"name": "contents", "required": "write", "granted": "write", "ok": true}
  ],
  "token": {"minted_at": "…", "expires_at": "…", "last_error": "", "last_error_at": null},
  "checked_at": "…"
}
```

`failed_step` is one of `config`, `app`, `installation`, `token`,
`repository` (token mode), or empty. `app`, `installation_id`, `permissions`
and `token` are present only in mode `app` and only as far as the check got.
`ok` is true when no step failed; missing permissions leave `ok` true and
are read from the `permissions` rows. The response never contains the key, a
JWT or a token. The route is a GET with no side effect a user cares about,
so "Test connection" is the same request again; no POST is added
**(my call)**.

`/api/release/status` and every other release route are unchanged.

The check is handed to the router as its own dependency, separate from
`ReleaseService`, because it must answer in mode `off`, where the release
service is nil. `ReleaseConfig.Enabled()` becomes true for every mode except
`off`, including a misconfigured App pair **(my call)**.

## 7. Frontend

A "GitHub connection" card in Settings → General, between the Service card
and the Access card, styled like them **(my call on placement)**. It loads
when the General sub-tab is shown and has a "Test connection" button that
reloads it. While loading the button is disabled.

Mode `app`, healthy:

```
GitHub connection                                   [ Test connection ]
The Release tab acts on GitHub as this app.

  ● Connected      Percona OBS Release (percona-obs-release) · app 123456
  Repository       percona/obs-packaging · installation 7654321
  Permissions      contents write ✓   pull_requests write ✓
                   actions write ✓    checks read ✓
  Token            issued 14:02, expires 15:02
  Checked          14:31:07
```

Mode `app`, failing (one line names the failed step in plain words, then
the sanitised error):

```
  ● Not connected  The app is not installed on percona/obs-packaging.
                   github GET /repos/percona/obs-packaging/installation: HTTP 404
```

Plain-word messages per `failed_step`: `config` shows the configuration
error as returned; `app` "GitHub rejected the app's key or ID."; 
`installation` "The app is not installed on <repo>." for a 404, otherwise
"Could not look up the app's installation."; `token` "GitHub would not
issue a token."; `repository` "The token cannot reach <repo>.".

A missing permission is shown in the row with the granted level (or "none")
in the warning colour, plus one line: "Some release actions will fail until
the app is granted these permissions."

Mode `token`: "Using a personal access token. Set RELEASE_GITHUB_APP_ID and
RELEASE_GITHUB_APP_PRIVATE_KEY to switch to the GitHub App.", the
repository, and reachable or not.

Mode `off`: "Not configured. The Release tab is hidden." and the two env
variable names.

A failed request to the route itself is shown as an error with the button
still available, like the other Settings cards.

## 8. Documentation

- `config.yaml.example`, `.env.example`, `README.md` (Release section) and
  `deploy/host/README.md`: the two new variables, the `base64 -w0` command,
  the required App permissions, the precedence over the token, and that the
  token is now the fallback.
- `2026-11-11-release-tab-design.md` § 5.1: a short note pointing here.

No host-files change, so no host-files version bump.

## 9. Known unknowns

To be checked against the real App before the personal token is removed
from production. Neither blocks this change, because the token fallback
remains:

1. **Approving held runs.** The dashboard approves held `pull_request` runs
   (`Service.approveHeld`). Whether GitHub accepts that approval from an App
   installation is unverified. If it is refused, the release shows the
   existing "Waiting for approval on GitHub" state with a link, and a person
   approves on GitHub.
2. **Conditional requests across token changes.** The ETag cache keeps
   refreshes nearly free. If GitHub's ETags differ per token, each hourly
   token change costs one round of uncached requests (on the order of a
   hundred), well inside the installation's hourly limit. No code is planned
   for this.
3. **The required-permission list** (§ 6.2) is derived from reading the
   code, not from GitHub refusing calls. On 2026-11-23 the deployment
   tooling confirmed that the installed App grants every permission on the
   list **(user)**; whether the list is sufficient for every release action
   is still to be seen in a real release.

Checked on 2026-11-23: none of the obs-packaging workflows branch on who the
actor is (`github.actor` appears only as a registry login name in
`build-obs-image.yml`), and `main` had no branch protection on 2026-11-11,
so nothing there depends on the dashboard acting as a person.
`Service.approveHeld` matches runs caused by the cut workflow's own push
(`github-actions[bot]`); that actor does not change, so the match stays as
it is.

Behaviour that changes on GitHub and is accepted: PRs, labels, comments,
merges and API-created commits appear as `<app-slug>[bot]` instead of the
token's owner. Events caused by an installation token do start workflow
runs, so the label-driven flow keeps working.

## 10. Left to the builder

- Names and shapes of the token source, the check and their types.
- Whether the check lives on `release.Service`, on the client, or beside
  them, and how `main.go` wires the three modes.
- Test layout. Expected coverage: config parsing for every row of the § 5
  table; JWT claims and signature; mint, reuse, refresh before expiry and
  drop on 401; not-installed; each `failed_step`; the permission comparison;
  the route's JSON in each mode and that no secret appears in it; the card's
  four states.
