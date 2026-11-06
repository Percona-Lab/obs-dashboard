# Authentication: Google sign-in with an allowlist

Date: 2026-11-06. Status: approved design, not yet implemented.

## 1. Purpose and scope

Anyone can read the dashboard without signing in. Actions that change state (today: triggering an OBS rebuild and toggling telemetry; later: further actions) must only be available to signed-in, allowlisted Percona users. The backend enforces this; the frontend hides controls the visitor cannot use.

In scope: Google OIDC sign-in, server-side sessions in SQLite, an allowlist stored in SQLite and managed from a new Settings tab, admins pinned in config/env, protecting `POST /api/rebuild` and `POST /api/telemetry`, hiding the Rebuild button for signed-out visitors.

Out of scope: roles or per-action permissions (every allowlisted user can perform every action and manage the allowlist); requiring login to read; any identity provider other than Google; a UI for telemetry; settings other than the allowlist.

Reference: Percona-Lab/laserfocus uses the same server-side Google OAuth flow (OmniAuth `google_oauth2`, `GOOGLE_CLIENT_ID`/`GOOGLE_CLIENT_SECRET`, an email/domain allowlist). This design follows it, with the allowlist in the database, stored sessions, and verified `email_verified`/`hd` claims.

## 2. Decisions

| # | Decision |
|---|---|
| D1 | Google OIDC authorization-code flow run by the backend, using `golang.org/x/oauth2` and `github.com/coreos/go-oidc/v3`. No Google JavaScript in the frontend; no auth proxy. |
| D2 | A user is allowed when the verified ID token has `email_verified=true`, `hd="percona.com"`, and the lowercased email is in `auth.admins` (config/env) or in the `allowed_users` table. |
| D3 | One access level. Every allowed user can use every protected action and manage the allowlist. |
| D4 | Config admins are always allowed and cannot be removed from the UI; they guarantee recovery from an empty or broken allowlist. |
| D5 | Sessions are rows in SQLite referenced by an opaque random cookie. The allow rule (D2, minus token claims) is re-checked on every session load, so removal takes effect immediately. Removing a user also deletes their sessions. |
| D6 | Reads stay public. Only routes behind `RequireUser` need a session. |
| D7 | If `GOOGLE_CLIENT_ID` is unset, auth is disabled: protected routes return 401, `/api/me` reports `auth_enabled:false`, and no sign-in UI is shown. The app fails closed. |
| D8 | CSRF: the session cookie is `SameSite=Lax`, and every state-changing request (POST/PUT/DELETE) carrying a session must have an `Origin` equal to `auth.public_url`. |
| D9 | Settings is a fourth `mainTab` value (`settings`), visible only when signed in, laid out as stacked zones. It starts with one zone: Access. |

## 3. Backend

### 3.1 Package `internal/auth`

- `Provider`: built from client ID, secret and `public_url`. Holds the `oauth2.Config` (redirect `public_url + "/auth/callback"`, scopes `openid email profile`, `hd=percona.com` auth-code option as a hint) and the `*oidc.IDTokenVerifier`. Methods: `AuthCodeURL(state, nonce)`, `Exchange(ctx, code) (Claims, error)` which exchanges the code, extracts `id_token`, verifies it, and returns `email`, `email_verified`, `hd`, `nonce`. Constructed with `oidc.NewProvider(ctx, "https://accounts.google.com")` in production; tests construct it with a custom endpoint and an `oidc.StaticKeySet`.
- `Store` (over `*sql.DB`): `ListUsers`, `AddUser(email, addedBy)`, `RemoveUser(email)` (deletes the user's sessions in the same transaction), `CreateSession(email, ttl) (id)`, `SessionEmail(id) (email, ok)` (ignores and deletes expired rows), `DeleteSession(id)`, `PruneExpired()`.
- `Authz`: holds the config admin set and the `Store`; `IsAllowed(email) bool` applies D2's email part (admins or `allowed_users`).
- Middleware:
  - `LoadUser`: reads cookie `obsboard_session`, resolves it via `Store.SessionEmail`, checks `Authz.IsAllowed`, and stores the email in the request context. Anonymous otherwise. Applied to every route.
  - `RequireUser`: 401 `unauthorized` when no user is in context.
  - `CheckOrigin`: for POST/PUT/DELETE with a user in context, 403 when `Origin` is not `public_url`.
- `UserFrom(ctx) (string, bool)` for handlers.

### 3.2 Schema

Added to the schema in `internal/store/db.go` with `CREATE TABLE IF NOT EXISTS`:

```sql
CREATE TABLE IF NOT EXISTS allowed_users (
  email    TEXT PRIMARY KEY,
  added_by TEXT NOT NULL,
  added_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS sessions (
  id         TEXT PRIMARY KEY,   -- 32 random bytes, base64url
  email      TEXT NOT NULL,
  created_at TEXT NOT NULL,
  expires_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS sessions_email ON sessions(email);
```

Timestamps are bound as RFC3339Nano strings in UTC, never as raw `time.Time` (modernc driver rule used across this repo). `PruneExpired` runs once at startup.

### 3.3 Configuration

```yaml
auth:
  public_url: "https://dashboard.obs.pg.labs.percona.com"
  admins: ["ricardo.dias@percona.com"]
  session_ttl: "168h"
```

Env: `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET` (secrets, env only); `OBS_DASHBOARD_ADMINS` (comma-separated, merged with `auth.admins`). Emails are lowercased on load. `public_url` is required when `GOOGLE_CLIENT_ID` is set; startup fails otherwise. For local dev, `public_url` is `http://localhost:5173` and the Vite proxy forwards `/auth` as it does `/api`.

### 3.4 Routes

| Route | Access | Behaviour |
|---|---|---|
| `GET /auth/login` | public | Sets short-lived (10 min) `HttpOnly` cookies holding random `state` and `nonce`, redirects to Google. |
| `GET /auth/callback` | public | Checks `state`, exchanges the code, verifies token and `nonce`, applies D2, creates a session, sets `obsboard_session` (`HttpOnly`, `Secure` when `public_url` is https, `SameSite=Lax`, `Path=/`, `Max-Age=session_ttl`), clears the state/nonce cookies, redirects to `/`. |
| `POST /auth/logout` | user | Deletes the session row, clears the cookie, 204. |
| `GET /api/me` | public | `{"email": "...", "auth_enabled": true}` or `{"email": null, "auth_enabled": bool}`. |
| `POST /api/rebuild` | user | Unchanged handler, now behind `RequireUser`. |
| `POST /api/telemetry` | user | Unchanged handler, now behind `RequireUser`. |
| `GET /api/settings/users` | user | `{"admins": [...], "users": [{"email","added_by","added_at"}]}` |
| `POST /api/settings/users` | user | Body `{"email"}`. Lowercases; 400 unless it ends in `@percona.com`; 409 if it is already a user or an admin; 201 with the row. `added_by` is the caller. |
| `DELETE /api/settings/users/{email}` | user | 400 when removing a config admin or oneself; 404 when absent; 204 otherwise. |

All other existing routes, including `POST /api/presence` and `POST /api/artifacts/metadata`, stay public. `CheckOrigin` applies to the user-only POST/DELETE routes. Future actions are added behind `RequireUser`.

When auth is disabled (D7), `/auth/*` routes return 404 and `RequireUser` returns 401.

### 3.5 Error handling

| Case | Behaviour |
|---|---|
| Client ID unset | Auth disabled, one Info log at startup. |
| Client ID set, OIDC discovery fails at startup | Startup fails. |
| Missing or mismatched `state`, failed exchange, invalid token, bad `nonce` | Redirect `/?auth_error=failed`, Warn log. |
| `email_verified` false, wrong `hd`, or email not allowed | Redirect `/?auth_error=not_allowed&email=<email>`, Info log. |
| Unknown or expired session | Treated as anonymous. |
| State-changing request with a user and a wrong/missing `Origin` | 403. |

## 4. Frontend

- `composables/useAuth.ts`: module-level singleton. Loads `GET /api/me` once at startup. Exposes `user`, `authEnabled`, `signedIn` (computed), `login()` (navigates to `/auth/login`), `logout()` (POST `/auth/logout`, then clears `user`), `handleUnauthorized()` (clears `user` and raises the toast "Session expired, sign in again").
- `AppHeader.vue`: when `authEnabled` and signed out, a "Sign in" button beside the theme toggle. When signed in, the email, a "Settings" tab pill, and "Sign out". When auth is disabled, nothing new. On load, an `auth_error` query parameter produces a one-time toast ("<email> is not allowed to sign in" or "Sign-in failed") and is removed from the URL.
- Rebuild: the Rebuild button renders only when `signedIn`. `useRebuild.trigger` calls `handleUnauthorized()` on a 401 instead of recording an error.
- Routing: `mainTab` gains `'settings'`. `useUrlState` accepts `?tab=settings`. When not signed in (including after sign-out), `settings` falls back to `overview`.
- `components/SettingsPanel.vue`: stacked zone layout. The Access zone has:
  - Admins from config, as read-only rows marked "config".
  - Allowed users, one row each with email, "added by <email> · <date>", and Remove (native `confirm`). Remove is disabled on the signed-in user's own row.
  - An email input and Add button. Server errors (400/409) are shown under the input.
- `composables/useSettingsUsers.ts`: `list()`, `add(email)`, `remove(email)`. A 401 calls `handleUnauthorized()`.
- `vite.config.ts`: proxy `/auth` to the backend alongside `/api`.

## 5. Testing

Go:
- `auth.Store`: add/list/remove; remove deletes that user's sessions; expired sessions are not returned and are deleted; `PruneExpired`.
- `auth.Authz.IsAllowed`: config admin, DB user, unknown email, case-insensitivity.
- Callback flow without mocking our own code: an `httptest` server acts as the token endpoint and returns an ID token signed by a test RSA key; the verifier uses `oidc.StaticKeySet`. Cases: success sets the session cookie and redirects to `/`; bad state; wrong `hd`; `email_verified=false`; email not allowed; bad nonce.
- Router: `/api/rebuild` and `/api/telemetry` return 401 without a session and reach the handler with one; bad `Origin` returns 403; GET routes and `/api/presence` remain public; auth disabled returns 401 on protected routes and 404 on `/auth/login`.
- Settings users endpoints: list, add (201, 400 wrong domain, 409 duplicate or admin), delete (204, 400 self, 400 admin, 404 absent).

Vitest:
- `useAuth`: loads `/api/me`; `handleUnauthorized` clears the user.
- `AppHeader`: signed-out, signed-in and auth-disabled states; `auth_error` toast.
- Rebuild button hidden when signed out, shown when signed in.
- `SettingsPanel`: renders admins and users; admins have no Remove; own row's Remove disabled; add and remove call the composable; server errors shown.
- `useUrlState`: `?tab=settings` while signed out resolves to `overview`.

Gates: `go test ./...` and `npm run build` and `npm test` pass. A manual sign-in against a real Google OAuth client on local dev (sign in, rebuild, add and remove a user, sign out) is a user-gated check, run once the OAuth client exists.

## 6. Rollout

- The schema change only adds tables; the previous image ignores them, so rolling back is safe.
- New Go dependencies: `golang.org/x/oauth2`, `github.com/coreos/go-oidc/v3` (pulls in `github.com/go-jose/go-jose/v4`).
- Google Cloud: a dedicated OAuth client (Web application), ideally in the same project as laserfocus (pending confirmation from its owner). Authorized redirect URIs: `https://dashboard.obs.pg.labs.percona.com/auth/callback` and `http://localhost:5173/auth/callback`.
- Prod: `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`, `OBS_DASHBOARD_ADMINS` in `.env` (single-quote values containing `$`); `auth.public_url` in `data/config.yaml`.
- Sign-in works only once the dashboard is served at `public_url`. Until the OAuth client exists and the dashboard is on that host, prod runs with auth disabled and Rebuild is unavailable from the UI. This gap is accepted.
- README gains an "Authentication" section: creating the OAuth client, redirect URIs, env vars, config keys, and the auth-disabled behaviour.
