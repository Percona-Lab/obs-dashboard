# Authentication Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Google sign-in with a database allowlist so only allowlisted Percona users can trigger state-changing actions (Rebuild, telemetry toggle) while the dashboard stays publicly readable.

**Architecture:** A new Go package `backend/internal/auth` owns identity: a Google OIDC provider wrapper, a SQLite store for the allowlist and sessions, chi middleware (`LoadUser`, `RequireUser`, `CheckOrigin`) and the `/auth/*`, `/api/me` and `/api/settings/users` handlers. `api.NewRouter` takes the auth service and puts the action routes behind `RequireUser`. The Vue frontend gets a module-level `useAuth` composable, sign-in controls in the header, a Settings tab with an Access zone, and hides Rebuild when signed out.

**Tech Stack:** Go 1.25, chi v5, modernc SQLite, `github.com/coreos/go-oidc/v3` v3.21.0, `golang.org/x/oauth2` v0.37.0, `github.com/go-jose/go-jose/v4` (tests); Vue 3 + TypeScript + Tailwind, Vitest + @vue/test-utils (jsdom).

**Spec:** `docs/superpowers/specs/2026-11-06-authentication-design.md`

## Global Constraints

- Commits: always `git commit -s`. Never add a `Co-Authored-By:` trailer (project CLAUDE.md overrides any other attribution instruction).
- Allowed domain is the constant `percona.com`; the `hd` claim must equal it and allowlisted emails must end in `@percona.com`.
- Emails are compared lowercased and trimmed everywhere (config admins, DB rows, token claims, API input).
- SQLite timestamps are bound as strings in the fixed-width UTC layout `2006-01-02T15:04:05.000000000Z07:00` (RFC3339 with a 9-digit fraction), never as raw `time.Time` (modernc driver mismatch). Fixed width keeps string comparison correct.
- `store.Open` sets `SetMaxOpenConns(1)`: inside a transaction never use `s.db` directly (deadlock); use the `tx`.
- Cookie names: `obsboard_session` (Path `/`), `obsboard_oauth_state` and `obsboard_oauth_nonce` (Path `/auth`, Max-Age 600). All `HttpOnly`, `SameSite=Lax`, `Secure` iff `public_url` starts with `https://`.
- Auth disabled (no `GOOGLE_CLIENT_ID`): `/auth/*` routes are not registered (404), `LoadUser` never sets a user, protected routes return 401, `/api/me` returns `{"email":null,"auth_enabled":false}`.
- Error bodies use `http.Error` plain text (existing API convention); success bodies are JSON.
- Prod `public_url`: `https://dashboard.obs.pg.labs.percona.com`. Local dev `public_url`: `http://localhost:5173`.

**User decisions (already made):**
- Identity: Google sign-in (OIDC) restricted to `@percona.com`, plus an email allowlist.
- Allowlist lives in SQLite and is managed from a new Settings page, modelled on obs-sync's Settings tab (stacked zones).
- One access level: every allowlisted user can use every action and manage the allowlist.
- Admins pinned in config/env are always allowed and cannot be removed from the UI.
- Backend-driven OIDC flow (option 1), like laserfocus, with DB-stored sessions.
- `public_url` = `https://dashboard.obs.pg.labs.percona.com`; the dashboard moves to that host later.
- Rebuild being unavailable in prod until the OAuth client exists and the host moves is accepted.
- The Google Cloud project is being confirmed with laserfocus's author; not a code dependency.

## Review Focus

1. A Google account whose token email has capitals (`Alice@Percona.com`) while the allowlist holds `alice@percona.com` must sign in. Test: Task 4 `TestCallbackAdminCaseInsensitive`.
2. A session exactly at or past its expiry must be anonymous; timestamp string comparison must not break on fractional-second width. Test: Task 1 `TestSessionExpiryBoundary` and Task 4 `TestExpiredSessionIsAnonymous`.
3. A user removed from the allowlist while signed in elsewhere must lose access on their next request. Test: Task 5 `TestRemovedUserLosesAccess`.
4. The user cancelling on Google's consent screen (`/auth/callback?error=access_denied`) must land on `/?auth_error=failed`, not a 500. Test: Task 4 `TestCallbackProviderError`.
5. Removing a user via an encoded or mixed-case path (`/api/settings/users/Bob%40Percona.com`) must remove `bob@percona.com`. Test: Task 5 `TestRemoveUserNormalizesPath`.

---

### Task 1: Auth schema and SQLite store

**Goal:** Add the `allowed_users` and `sessions` tables and an `auth.Store` with allowlist and session operations.

**Files:**
- Modify: `backend/internal/store/db.go` (the `schema` constant, before its closing backtick at ~line 99)
- Create: `backend/internal/auth/store.go`
- Test: `backend/internal/auth/store_test.go`

**Acceptance Criteria:**
- [ ] `store.Open(":memory:")` creates `allowed_users`, `sessions` and index `sessions_email`.
- [ ] `AddUser` returns `ErrExists` for a duplicate; `ListUsers` is ordered by email and returns `[]User{}` (not nil) when empty.
- [ ] `RemoveUser` deletes the user and all their sessions in one transaction; returns `ErrNotFound` when absent.
- [ ] `SessionEmail` returns ok=false and deletes the row once `now >= expires_at`.
- [ ] `PruneExpired` deletes only expired sessions and returns the count.

**Verify:** `cd backend && go test ./internal/auth/ -run 'Store|Session|Prune' -v` → all PASS

**Steps:**

- [ ] **Step 1: Add the tables to the schema.** In `backend/internal/store/db.go`, append inside the `schema` raw string, after `CREATE INDEX IF NOT EXISTS idx_metrics_samples_ts ON metrics_samples (ts);`:

```sql

CREATE TABLE IF NOT EXISTS allowed_users (
    email    TEXT PRIMARY KEY,
    added_by TEXT NOT NULL,
    added_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS sessions (
    id         TEXT PRIMARY KEY,
    email      TEXT NOT NULL,
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS sessions_email ON sessions (email);
```

- [ ] **Step 2: Write the failing tests** in `backend/internal/auth/store_test.go`:

```go
package auth

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/percona/obs-dashboard/internal/store"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

var t0 = time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

func TestStoreAddListUsers(t *testing.T) {
	s := NewStore(newTestDB(t))
	users, err := s.ListUsers()
	if err != nil || users == nil || len(users) != 0 {
		t.Fatalf("empty list: users=%v err=%v", users, err)
	}
	if _, err := s.AddUser("bob@percona.com", "alice@percona.com", t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddUser("ann@percona.com", "alice@percona.com", t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddUser("bob@percona.com", "x@percona.com", t0); !errors.Is(err, ErrExists) {
		t.Fatalf("duplicate add: want ErrExists, got %v", err)
	}
	users, err = s.ListUsers()
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 2 || users[0].Email != "ann@percona.com" || users[1].Email != "bob@percona.com" {
		t.Fatalf("unexpected users: %+v", users)
	}
	if users[1].AddedBy != "alice@percona.com" || users[1].AddedAt != "2026-11-06T12:00:00.000000000Z" {
		t.Fatalf("unexpected row: %+v", users[1])
	}
	ok, err := s.HasUser("bob@percona.com")
	if err != nil || !ok {
		t.Fatalf("HasUser: %v %v", ok, err)
	}
}

func TestStoreRemoveUserDeletesSessions(t *testing.T) {
	s := NewStore(newTestDB(t))
	if _, err := s.AddUser("bob@percona.com", "alice@percona.com", t0); err != nil {
		t.Fatal(err)
	}
	sid, err := s.CreateSession("bob@percona.com", t0, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveUser("bob@percona.com"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.SessionEmail(sid, t0); ok {
		t.Fatal("session survived user removal")
	}
	if err := s.RemoveUser("bob@percona.com"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second remove: want ErrNotFound, got %v", err)
	}
}

func TestSessionRoundTrip(t *testing.T) {
	s := NewStore(newTestDB(t))
	sid, err := s.CreateSession("bob@percona.com", t0, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(sid) < 40 {
		t.Fatalf("session id too short: %q", sid)
	}
	email, ok, err := s.SessionEmail(sid, t0.Add(59*time.Minute))
	if err != nil || !ok || email != "bob@percona.com" {
		t.Fatalf("lookup: %q %v %v", email, ok, err)
	}
	if _, ok, _ := s.SessionEmail("nope", t0); ok {
		t.Fatal("unknown id resolved")
	}
	if err := s.DeleteSession(sid); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.SessionEmail(sid, t0); ok {
		t.Fatal("deleted session resolved")
	}
}

// Review focus 2: expiry is exact and string comparison is width-safe.
func TestSessionExpiryBoundary(t *testing.T) {
	s := NewStore(newTestDB(t))
	start := t0.Add(123456789 * time.Nanosecond) // non-zero fraction
	sid, err := s.CreateSession("bob@percona.com", start, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.SessionEmail(sid, start.Add(time.Hour-time.Nanosecond)); !ok {
		t.Fatal("session expired early")
	}
	if _, ok, _ := s.SessionEmail(sid, start.Add(time.Hour)); ok {
		t.Fatal("session valid at expires_at")
	}
	// The expired row was deleted: even an earlier clock no longer finds it.
	if _, ok, _ := s.SessionEmail(sid, start); ok {
		t.Fatal("expired session row not deleted")
	}
}

func TestPruneExpired(t *testing.T) {
	s := NewStore(newTestDB(t))
	old, _ := s.CreateSession("a@percona.com", t0, time.Minute)
	fresh, _ := s.CreateSession("b@percona.com", t0, 2*time.Hour)
	n, err := s.PruneExpired(t0.Add(time.Hour))
	if err != nil || n != 1 {
		t.Fatalf("prune: n=%d err=%v", n, err)
	}
	if _, ok, _ := s.SessionEmail(fresh, t0.Add(time.Hour)); !ok {
		t.Fatal("fresh session pruned")
	}
	if _, ok, _ := s.SessionEmail(old, t0); ok {
		t.Fatal("old session not pruned")
	}
}
```

- [ ] **Step 3: Run to verify failure.** `cd backend && go test ./internal/auth/ -v` → FAIL to compile (`undefined: NewStore`).

- [ ] **Step 4: Implement** `backend/internal/auth/store.go`:

```go
// Package auth owns sign-in: the Google OIDC flow, the allowlist, sessions,
// and the HTTP middleware that gates state-changing routes.
package auth

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"time"
)

// tsLayout is RFC3339 with a fixed nine-digit fraction. Always formatted in
// UTC, so stored timestamps compare correctly as strings. Never bind a raw
// time.Time: the modernc driver stores it in a different format.
const tsLayout = "2006-01-02T15:04:05.000000000Z07:00"

func ts(t time.Time) string { return t.UTC().Format(tsLayout) }

var (
	ErrNotFound = errors.New("not found")
	ErrExists   = errors.New("already exists")
)

// User is one allowlist row.
type User struct {
	Email   string `json:"email"`
	AddedBy string `json:"added_by"`
	AddedAt string `json:"added_at"`
}

// Store persists the allowlist and sessions.
type Store struct{ db *sql.DB }

func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// randomToken returns 32 random bytes, base64url without padding.
func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func (s *Store) ListUsers() ([]User, error) {
	rows, err := s.db.Query(`SELECT email, added_by, added_at FROM allowed_users ORDER BY email`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := []User{}
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.Email, &u.AddedBy, &u.AddedAt); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func (s *Store) HasUser(email string) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM allowed_users WHERE email = ?`, email).Scan(&n)
	return n > 0, err
}

// AddUser inserts an allowlist row; ErrExists when the email is already there.
func (s *Store) AddUser(email, addedBy string, now time.Time) (User, error) {
	u := User{Email: email, AddedBy: addedBy, AddedAt: ts(now)}
	res, err := s.db.Exec(
		`INSERT INTO allowed_users (email, added_by, added_at) VALUES (?, ?, ?) ON CONFLICT(email) DO NOTHING`,
		u.Email, u.AddedBy, u.AddedAt)
	if err != nil {
		return User{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return User{}, ErrExists
	}
	return u, nil
}

// RemoveUser deletes the allowlist row and every session of that email.
func (s *Store) RemoveUser(email string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`DELETE FROM allowed_users WHERE email = ?`, email)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if _, err := tx.Exec(`DELETE FROM sessions WHERE email = ?`, email); err != nil {
		return err
	}
	return tx.Commit()
}

// CreateSession stores a new session and returns its opaque id.
func (s *Store) CreateSession(email string, now time.Time, ttl time.Duration) (string, error) {
	id, err := randomToken()
	if err != nil {
		return "", err
	}
	_, err = s.db.Exec(`INSERT INTO sessions (id, email, created_at, expires_at) VALUES (?, ?, ?, ?)`,
		id, email, ts(now), ts(now.Add(ttl)))
	if err != nil {
		return "", err
	}
	return id, nil
}

// SessionEmail resolves an unexpired session. An expired row is deleted.
func (s *Store) SessionEmail(id string, now time.Time) (string, bool, error) {
	var email, expires string
	err := s.db.QueryRow(`SELECT email, expires_at FROM sessions WHERE id = ?`, id).Scan(&email, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if expires <= ts(now) {
		_, err := s.db.Exec(`DELETE FROM sessions WHERE id = ?`, id)
		return "", false, err
	}
	return email, true, nil
}

func (s *Store) DeleteSession(id string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE id = ?`, id)
	return err
}

// PruneExpired deletes every session whose expiry is at or before now.
func (s *Store) PruneExpired(now time.Time) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM sessions WHERE expires_at <= ?`, ts(now))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
```

- [ ] **Step 5: Run tests.** `cd backend && go test ./internal/auth/ ./internal/store/ -v` → PASS (store package tests still pass with the new tables).

- [ ] **Step 6: Commit.**

```bash
git add backend/internal/store/db.go backend/internal/auth/store.go backend/internal/auth/store_test.go
git commit -s -m "feat(auth): allowlist and session tables with SQLite store"
```

```json:metadata
{"files": ["backend/internal/store/db.go", "backend/internal/auth/store.go", "backend/internal/auth/store_test.go"], "verifyCommand": "cd backend && go test ./internal/auth/ ./internal/store/ -v", "acceptanceCriteria": ["store.Open creates allowed_users, sessions, sessions_email", "AddUser ErrExists on duplicate; ListUsers ordered, empty slice not nil", "RemoveUser deletes user and sessions in one tx; ErrNotFound when absent", "SessionEmail ok=false and row deleted once now >= expires_at", "PruneExpired deletes only expired and returns count"], "modelTier": "mechanical"}
```

---

### Task 2: Auth configuration

**Goal:** Load `auth.public_url`, `auth.admins`, `auth.session_ttl` and the Google credentials into `config.Config.Auth`, with validation when sign-in is enabled.

**Files:**
- Modify: `backend/internal/config/config.go`
- Test: `backend/internal/config/config_test.go`

**Acceptance Criteria:**
- [ ] Defaults: `SessionTTL` 168h, `Enabled()` false, `Admins` empty.
- [ ] `OBS_DASHBOARD_ADMINS="a@percona.com, B@percona.com"` merges with YAML `auth.admins`; entries are trimmed, empty ones dropped (lowercasing happens in `auth.NewService`).
- [ ] `GOOGLE_CLIENT_ID` set without `GOOGLE_CLIENT_SECRET` or without `public_url` → `Load()` error naming the missing key.
- [ ] `public_url` that is not an absolute http(s) URL → error; a trailing `/` is trimmed.

**Verify:** `cd backend && go test ./internal/config/ -run Auth -v` → all PASS

**Steps:**

- [ ] **Step 1: Write the failing tests** (append to `backend/internal/config/config_test.go`):

```go
func TestAuthDefaults(t *testing.T) {
	t.Setenv("OBS_USERNAME", "u")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Auth.SessionTTL != 168*time.Hour {
		t.Errorf("session ttl: got %v", cfg.Auth.SessionTTL)
	}
	if cfg.Auth.Enabled() {
		t.Error("auth enabled without GOOGLE_CLIENT_ID")
	}
	if len(cfg.Auth.Admins) != 0 {
		t.Errorf("admins: %v", cfg.Auth.Admins)
	}
}

func TestAuthAdminsFromFileAndEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("auth:\n  admins: [\"root@percona.com\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONFIG_FILE", path)
	t.Setenv("OBS_USERNAME", "u")
	t.Setenv("OBS_DASHBOARD_ADMINS", " a@percona.com, ,B@percona.com ")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"root@percona.com", "a@percona.com", "B@percona.com"}
	if strings.Join(cfg.Auth.Admins, ",") != strings.Join(want, ",") {
		t.Errorf("admins: got %v want %v", cfg.Auth.Admins, want)
	}
}

func TestAuthEnabledValidation(t *testing.T) {
	t.Setenv("OBS_USERNAME", "u")
	t.Setenv("GOOGLE_CLIENT_ID", "id")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "GOOGLE_CLIENT_SECRET") {
		t.Fatalf("missing secret: got %v", err)
	}
	t.Setenv("GOOGLE_CLIENT_SECRET", "secret")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "auth.public_url") {
		t.Fatalf("missing public_url: got %v", err)
	}
	t.Setenv("AUTH_PUBLIC_URL", "dashboard.example")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "auth.public_url") {
		t.Fatalf("relative public_url: got %v", err)
	}
	t.Setenv("AUTH_PUBLIC_URL", "https://dashboard.obs.pg.labs.percona.com/")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Auth.Enabled() || cfg.Auth.PublicURL != "https://dashboard.obs.pg.labs.percona.com" {
		t.Fatalf("auth: %+v", cfg.Auth)
	}
	if cfg.Auth.GoogleClientID != "id" || cfg.Auth.GoogleClientSecret != "secret" {
		t.Fatalf("credentials not loaded: %+v", cfg.Auth)
	}
}
```

- [ ] **Step 2: Run to verify failure.** `cd backend && go test ./internal/config/ -run Auth -v` → FAIL (`cfg.Auth undefined`).

- [ ] **Step 3: Implement.** In `config.go`:

Add the field to `Config` after `Idle IdleConfig`:

```go
	Auth       AuthConfig
```

Add the type next to `IdleConfig`:

```go
// AuthConfig configures Google sign-in. Sign-in is enabled only when
// GoogleClientID is set; otherwise protected actions are unavailable.
type AuthConfig struct {
	PublicURL          string
	Admins             []string
	SessionTTL         time.Duration
	GoogleClientID     string
	GoogleClientSecret string
}

// Enabled reports whether Google sign-in is configured.
func (a AuthConfig) Enabled() bool { return a.GoogleClientID != "" }

// loadAuth reads the auth section. Admins come from auth.admins plus the
// comma-separated OBS_DASHBOARD_ADMINS env var.
func loadAuth(v *viper.Viper) (AuthConfig, error) {
	ttl, err := time.ParseDuration(v.GetString("auth.session_ttl"))
	if err != nil {
		return AuthConfig{}, fmt.Errorf("invalid AUTH_SESSION_TTL %q: %w", v.GetString("auth.session_ttl"), err)
	}
	a := AuthConfig{
		PublicURL:          strings.TrimRight(v.GetString("auth.public_url"), "/"),
		SessionTTL:         ttl,
		GoogleClientID:     os.Getenv("GOOGLE_CLIENT_ID"),
		GoogleClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
	}
	for _, e := range append(v.GetStringSlice("auth.admins"), strings.Split(os.Getenv("OBS_DASHBOARD_ADMINS"), ",")...) {
		if e = strings.TrimSpace(e); e != "" {
			a.Admins = append(a.Admins, e)
		}
	}
	if !a.Enabled() {
		return a, nil
	}
	if a.GoogleClientSecret == "" {
		return AuthConfig{}, fmt.Errorf("GOOGLE_CLIENT_SECRET is required when GOOGLE_CLIENT_ID is set")
	}
	u, err := url.Parse(a.PublicURL)
	if a.PublicURL == "" || err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return AuthConfig{}, fmt.Errorf("auth.public_url (or AUTH_PUBLIC_URL) must be an absolute http(s) URL when GOOGLE_CLIENT_ID is set, got %q", a.PublicURL)
	}
	return a, nil
}
```

In `Load()`: add `v.SetDefault("auth.session_ttl", "168h")` with the other defaults; add `{"auth.public_url", "AUTH_PUBLIC_URL"}` and `{"auth.session_ttl", "AUTH_SESSION_TTL"}` to the env bind list; after `cfg.LegacyRoot = legacy.root` add:

```go
	authCfg, err := loadAuth(v)
	if err != nil {
		return nil, err
	}
	cfg.Auth = authCfg
```

- [ ] **Step 4: Run tests.** `cd backend && go test ./internal/config/ -v` → PASS (all, including existing).

- [ ] **Step 5: Commit.**

```bash
git add backend/internal/config/config.go backend/internal/config/config_test.go
git commit -s -m "feat(config): auth section with Google credentials and admins"
```

```json:metadata
{"files": ["backend/internal/config/config.go", "backend/internal/config/config_test.go"], "verifyCommand": "cd backend && go test ./internal/config/ -v", "acceptanceCriteria": ["SessionTTL defaults to 168h, Enabled false, Admins empty", "OBS_DASHBOARD_ADMINS merged with auth.admins, trimmed, empties dropped", "GOOGLE_CLIENT_ID without secret or public_url fails Load naming the key", "non-absolute public_url fails; trailing slash trimmed"], "modelTier": "mechanical"}
```

---

### Task 3: Google OIDC provider wrapper

**Goal:** `auth.Provider` builds the Google authorization URL and exchanges a callback code for a verified `Identity`, testable against a fake token endpoint.

**Files:**
- Modify: `backend/go.mod`, `backend/go.sum`
- Create: `backend/internal/auth/provider.go`
- Create: `backend/internal/auth/fakegoogle_test.go` (shared test helper for Tasks 3–5)
- Test: `backend/internal/auth/provider_test.go`

**Acceptance Criteria:**
- [ ] `AuthCodeURL(state, nonce)` contains `state`, `nonce`, `hd=percona.com`, `scope=openid+email+profile` and the redirect URL.
- [ ] `Exchange` returns `Identity` with lowercased, trimmed email, `EmailVerified`, `HostedDomain`, `Nonce` from a token signed by the trusted key.
- [ ] `Exchange` errors for a token signed by another key, a wrong audience, an expired token, and a token response without `id_token`.

**Verify:** `cd backend && go test ./internal/auth/ -run Provider -v` → all PASS

**Steps:**

- [ ] **Step 1: Add dependencies.**

```bash
cd backend && go get github.com/coreos/go-oidc/v3@v3.21.0 golang.org/x/oauth2@v0.37.0
```

- [ ] **Step 2: Write the shared fake** `backend/internal/auth/fakegoogle_test.go`:

```go
package auth

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/go-jose/go-jose/v4"
	"golang.org/x/oauth2"
)

const (
	testIssuer   = "https://accounts.google.com"
	testClientID = "client-123"
)

// fakeGoogle is a token endpoint that returns an ID token signed with key and
// carrying whatever claims the test set last.
type fakeGoogle struct {
	t       *testing.T
	srv     *httptest.Server
	key     *rsa.PrivateKey
	claims  map[string]any
	noIDTok bool
}

func newFakeGoogle(t *testing.T) *fakeGoogle {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeGoogle{t: t, key: key}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/token" {
			http.NotFound(w, r)
			return
		}
		body := map[string]any{"access_token": "at", "token_type": "Bearer", "expires_in": 3600}
		if !f.noIDTok {
			body["id_token"] = f.sign(f.key, f.claims)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeGoogle) sign(key *rsa.PrivateKey, claims map[string]any) string {
	f.t.Helper()
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, nil)
	if err != nil {
		f.t.Fatal(err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		f.t.Fatal(err)
	}
	obj, err := signer.Sign(payload)
	if err != nil {
		f.t.Fatal(err)
	}
	s, err := obj.CompactSerialize()
	if err != nil {
		f.t.Fatal(err)
	}
	return s
}

func (f *fakeGoogle) provider() *Provider {
	verifier := oidc.NewVerifier(testIssuer,
		&oidc.StaticKeySet{PublicKeys: []crypto.PublicKey{&f.key.PublicKey}},
		&oidc.Config{ClientID: testClientID})
	return NewProvider(oauth2.Config{
		ClientID:     testClientID,
		ClientSecret: "secret",
		RedirectURL:  "http://dash.test/auth/callback",
		Endpoint: oauth2.Endpoint{
			AuthURL:   f.srv.URL + "/auth",
			TokenURL:  f.srv.URL + "/token",
			AuthStyle: oauth2.AuthStyleInParams,
		},
		Scopes: []string{oidc.ScopeOpenID, "email", "profile"},
	}, verifier)
}

// validClaims are the claims of a verified Percona Google account.
func validClaims(email, nonce string) map[string]any {
	now := time.Now()
	return map[string]any{
		"iss": testIssuer, "aud": testClientID, "sub": "1",
		"iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
		"email": email, "email_verified": true, "hd": "percona.com", "nonce": nonce,
	}
}
```

- [ ] **Step 3: Write the failing tests** `backend/internal/auth/provider_test.go`:

```go
package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"net/url"
	"testing"
	"time"
)

func TestProviderAuthCodeURL(t *testing.T) {
	f := newFakeGoogle(t)
	u, err := url.Parse(f.provider().AuthCodeURL("st", "nn"))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	for k, want := range map[string]string{
		"state": "st", "nonce": "nn", "hd": "percona.com",
		"scope": "openid email profile", "redirect_uri": "http://dash.test/auth/callback",
		"client_id": testClientID, "response_type": "code",
	} {
		if got := q.Get(k); got != want {
			t.Errorf("%s: got %q want %q", k, got, want)
		}
	}
}

func TestProviderExchange(t *testing.T) {
	f := newFakeGoogle(t)
	f.claims = validClaims("  Alice@Percona.com ", "nn")
	id, err := f.provider().Exchange(context.Background(), "code")
	if err != nil {
		t.Fatal(err)
	}
	if id.Email != "alice@percona.com" || !id.EmailVerified || id.HostedDomain != "percona.com" || id.Nonce != "nn" {
		t.Fatalf("identity: %+v", id)
	}
}

func TestProviderExchangeRejects(t *testing.T) {
	other, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(f *fakeGoogle){
		"wrong key": func(f *fakeGoogle) {
			// Replace the served token with one signed by an untrusted key.
			trusted := f.key
			f.key = other
			t.Cleanup(func() { f.key = trusted })
		},
		"wrong audience": func(f *fakeGoogle) { f.claims["aud"] = "someone-else" },
		"expired": func(f *fakeGoogle) {
			f.claims["exp"] = time.Now().Add(-time.Hour).Unix()
		},
		"no id_token": func(f *fakeGoogle) { f.noIDTok = true },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFakeGoogle(t)
			p := f.provider() // verifier trusts the original key
			f.claims = validClaims("alice@percona.com", "nn")
			mutate(f)
			if _, err := p.Exchange(context.Background(), "code"); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}
```

- [ ] **Step 4: Run to verify failure.** `cd backend && go test ./internal/auth/ -run Provider -v` → FAIL to compile (`undefined: NewProvider`).

- [ ] **Step 5: Implement** `backend/internal/auth/provider.go`:

```go
package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// AllowedDomain is the only Google Workspace domain that may sign in.
const AllowedDomain = "percona.com"

const googleIssuer = "https://accounts.google.com"

// Identity is what a verified Google ID token says about the user.
type Identity struct {
	Email         string
	EmailVerified bool
	HostedDomain  string
	Nonce         string
}

// Provider runs the OIDC authorization-code flow against Google.
type Provider struct {
	oauth    oauth2.Config
	verifier *oidc.IDTokenVerifier
}

// NewProvider wires an OAuth2 config and ID token verifier. Tests use it with
// a fake token endpoint and a static key set.
func NewProvider(cfg oauth2.Config, verifier *oidc.IDTokenVerifier) *Provider {
	return &Provider{oauth: cfg, verifier: verifier}
}

// NewGoogleProvider discovers Google's endpoints and keys.
func NewGoogleProvider(ctx context.Context, clientID, clientSecret, redirectURL string) (*Provider, error) {
	p, err := oidc.NewProvider(ctx, googleIssuer)
	if err != nil {
		return nil, fmt.Errorf("google oidc discovery: %w", err)
	}
	return NewProvider(oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURL,
		Endpoint:     p.Endpoint(),
		Scopes:       []string{oidc.ScopeOpenID, "email", "profile"},
	}, p.Verifier(&oidc.Config{ClientID: clientID})), nil
}

// AuthCodeURL is where the browser is sent to sign in. hd is only a hint to
// Google's account chooser; the callback enforces the domain.
func (p *Provider) AuthCodeURL(state, nonce string) string {
	return p.oauth.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.SetAuthURLParam("hd", AllowedDomain))
}

// Exchange trades the callback code for a verified identity.
func (p *Provider) Exchange(ctx context.Context, code string) (Identity, error) {
	tok, err := p.oauth.Exchange(ctx, code)
	if err != nil {
		return Identity{}, fmt.Errorf("code exchange: %w", err)
	}
	raw, ok := tok.Extra("id_token").(string)
	if !ok || raw == "" {
		return Identity{}, errors.New("token response has no id_token")
	}
	idt, err := p.verifier.Verify(ctx, raw)
	if err != nil {
		return Identity{}, fmt.Errorf("verify id_token: %w", err)
	}
	var c struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		HD            string `json:"hd"`
	}
	if err := idt.Claims(&c); err != nil {
		return Identity{}, fmt.Errorf("id_token claims: %w", err)
	}
	return Identity{
		Email:         strings.ToLower(strings.TrimSpace(c.Email)),
		EmailVerified: c.EmailVerified,
		HostedDomain:  c.HD,
		Nonce:         idt.Nonce,
	}, nil
}
```

- [ ] **Step 6: Tidy and run tests.** `cd backend && go mod tidy && go test ./internal/auth/ -v` → PASS. `go.mod` must list `github.com/coreos/go-oidc/v3`, `golang.org/x/oauth2` and `github.com/go-jose/go-jose/v4` as requirements.

- [ ] **Step 7: Commit.**

```bash
git add backend/go.mod backend/go.sum backend/internal/auth/provider.go backend/internal/auth/provider_test.go backend/internal/auth/fakegoogle_test.go
git commit -s -m "feat(auth): Google OIDC provider wrapper"
```

```json:metadata
{"files": ["backend/go.mod", "backend/go.sum", "backend/internal/auth/provider.go", "backend/internal/auth/fakegoogle_test.go", "backend/internal/auth/provider_test.go"], "verifyCommand": "cd backend && go test ./internal/auth/ -run Provider -v", "acceptanceCriteria": ["AuthCodeURL carries state, nonce, hd=percona.com, scopes, redirect_uri", "Exchange returns lowercased trimmed email, verified flag, hd, nonce", "Exchange errors on untrusted key, wrong audience, expired token, missing id_token"], "modelTier": "standard"}
```

---

### Task 4: Auth service: middleware and sign-in handlers

**Goal:** `auth.Service` with `LoadUser`/`RequireUser`/`CheckOrigin` middleware and the `/auth/login`, `/auth/callback`, `/auth/logout`, `/api/me` handlers.

**Files:**
- Create: `backend/internal/auth/service.go`
- Test: `backend/internal/auth/service_test.go`

**Acceptance Criteria:**
- [ ] `/auth/login` sets `obsboard_oauth_state` and `obsboard_oauth_nonce` (HttpOnly, Path `/auth`, Max-Age 600) and 302s to the provider URL carrying that state and `hd=percona.com`.
- [ ] `/auth/callback` with matching state/nonce and an allowed identity sets `obsboard_session` (HttpOnly, Path `/`, SameSite Lax) and 302s to `/`.
- [ ] Provider `error` param, state mismatch, missing state cookie, nonce mismatch, failed exchange → 302 `/?auth_error=failed`.
- [ ] `email_verified=false`, `hd` other than `percona.com`, or an email not allowed → 302 `/?auth_error=not_allowed&email=<escaped>`.
- [ ] Protected route: no session → 401; session + `Origin: http://dash.test` → handler runs; session + other or missing Origin on POST → 403.
- [ ] Expired session → anonymous; `/auth/logout` deletes the session and clears the cookie (204).
- [ ] Disabled service: `/auth/login` 404, `/api/me` → `{"email":null,"auth_enabled":false}`, protected route 401 even with a valid session row.

**Verify:** `cd backend && go test ./internal/auth/ -v` → all PASS

**Steps:**

- [ ] **Step 1: Write the failing tests** `backend/internal/auth/service_test.go`:

```go
package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

const testOrigin = "http://dash.test"

type harness struct {
	t     *testing.T
	svc   *Service
	fake  *fakeGoogle
	h     http.Handler
	clock time.Time
}

// newHarness builds an enabled service (admins given) behind a router that
// also exposes POST /api/protected behind RequireUser+CheckOrigin.
func newHarness(t *testing.T, admins ...string) *harness {
	t.Helper()
	hs := &harness{t: t, fake: newFakeGoogle(t), clock: t0}
	hs.svc = NewService(newTestDB(t), Options{
		Provider:   hs.fake.provider(),
		PublicURL:  testOrigin,
		Admins:     admins,
		SessionTTL: time.Hour,
		Now:        func() time.Time { return hs.clock },
	})
	hs.h = testRouter(hs.svc)
	return hs
}

func testRouter(svc *Service) http.Handler {
	r := chi.NewRouter()
	r.Use(svc.LoadUser)
	svc.Mount(r)
	r.With(svc.RequireUser, svc.CheckOrigin).Post("/api/protected", func(w http.ResponseWriter, r *http.Request) {
		email, _ := UserFrom(r.Context())
		w.Header().Set("X-User", email)
		w.WriteHeader(http.StatusNoContent)
	})
	return r
}

func (hs *harness) do(req *http.Request, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	hs.h.ServeHTTP(rec, req)
	return rec
}

func cookieNamed(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// startLogin hits /auth/login and returns the state and nonce cookies.
func (hs *harness) startLogin() (state, nonce *http.Cookie) {
	rec := hs.do(httptest.NewRequest(http.MethodGet, "/auth/login", nil))
	state, nonce = cookieNamed(rec, stateCookie), cookieNamed(rec, nonceCookie)
	if rec.Code != http.StatusFound || state == nil || nonce == nil {
		hs.t.Fatalf("login: code=%d cookies=%v", rec.Code, rec.Result().Cookies())
	}
	return state, nonce
}

// callback completes sign-in with the given claims and returns the response.
func (hs *harness) callback(query string, claims map[string]any, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	hs.fake.claims = claims
	return hs.do(httptest.NewRequest(http.MethodGet, "/auth/callback?"+query, nil), cookies...)
}

// signIn runs the full flow for email and returns the session cookie.
func (hs *harness) signIn(email string) *http.Cookie {
	hs.t.Helper()
	st, nn := hs.startLogin()
	rec := hs.callback("code=c&state="+st.Value, validClaims(email, nn.Value), st, nn)
	sess := cookieNamed(rec, sessionCookie)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/" || sess == nil {
		hs.t.Fatalf("sign-in %s: code=%d location=%q", email, rec.Code, rec.Header().Get("Location"))
	}
	return sess
}

func (hs *harness) post(path string, origin string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, nil)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	return hs.do(req, cookies...)
}

func me(t *testing.T, rec *httptest.ResponseRecorder) (email *string, enabled bool) {
	t.Helper()
	var body struct {
		Email       *string `json:"email"`
		AuthEnabled bool    `json:"auth_enabled"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("me body %q: %v", rec.Body.String(), err)
	}
	return body.Email, body.AuthEnabled
}

func TestLoginRedirect(t *testing.T) {
	hs := newHarness(t, "alice@percona.com")
	rec := hs.do(httptest.NewRequest(http.MethodGet, "/auth/login", nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("code %d", rec.Code)
	}
	st := cookieNamed(rec, stateCookie)
	if st == nil || !st.HttpOnly || st.Path != "/auth" || st.MaxAge != 600 || st.SameSite != http.SameSiteLaxMode {
		t.Fatalf("state cookie: %+v", st)
	}
	loc, _ := url.Parse(rec.Header().Get("Location"))
	if !strings.HasPrefix(loc.String(), hs.fake.srv.URL+"/auth") || loc.Query().Get("state") != st.Value || loc.Query().Get("hd") != "percona.com" {
		t.Fatalf("location: %s", loc)
	}
}

// Review focus 1: mixed-case token email matches a lowercase admin.
func TestCallbackAdminCaseInsensitive(t *testing.T) {
	hs := newHarness(t, "Alice@percona.com")
	sess := hs.signIn("Alice@Percona.com")
	if !sess.HttpOnly || sess.Path != "/" || sess.SameSite != http.SameSiteLaxMode || sess.MaxAge != 3600 {
		t.Fatalf("session cookie: %+v", sess)
	}
	email, enabled := me(t, hs.do(httptest.NewRequest(http.MethodGet, "/api/me", nil), sess))
	if !enabled || email == nil || *email != "alice@percona.com" {
		t.Fatalf("me: %v %v", email, enabled)
	}
}

func TestCallbackAllowlistedUser(t *testing.T) {
	hs := newHarness(t, "alice@percona.com")
	if _, err := hs.svc.store.AddUser("bob@percona.com", "alice@percona.com", t0); err != nil {
		t.Fatal(err)
	}
	hs.signIn("bob@percona.com")
}

// Review focus 4: user cancels on Google.
func TestCallbackProviderError(t *testing.T) {
	hs := newHarness(t, "alice@percona.com")
	st, nn := hs.startLogin()
	rec := hs.callback("error=access_denied&state="+st.Value, nil, st, nn)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/?auth_error=failed" {
		t.Fatalf("code=%d location=%q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestCallbackFailures(t *testing.T) {
	cases := map[string]func(hs *harness) *httptest.ResponseRecorder{
		"state mismatch": func(hs *harness) *httptest.ResponseRecorder {
			st, nn := hs.startLogin()
			return hs.callback("code=c&state=other", validClaims("alice@percona.com", nn.Value), st, nn)
		},
		"missing state cookie": func(hs *harness) *httptest.ResponseRecorder {
			st, nn := hs.startLogin()
			return hs.callback("code=c&state="+st.Value, validClaims("alice@percona.com", nn.Value), nn)
		},
		"nonce mismatch": func(hs *harness) *httptest.ResponseRecorder {
			st, nn := hs.startLogin()
			return hs.callback("code=c&state="+st.Value, validClaims("alice@percona.com", "replayed"), st, nn)
		},
		"exchange fails": func(hs *harness) *httptest.ResponseRecorder {
			st, nn := hs.startLogin()
			hs.fake.noIDTok = true
			return hs.callback("code=c&state="+st.Value, validClaims("alice@percona.com", nn.Value), st, nn)
		},
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			hs := newHarness(t, "alice@percona.com")
			rec := run(hs)
			if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/?auth_error=failed" {
				t.Fatalf("code=%d location=%q", rec.Code, rec.Header().Get("Location"))
			}
			if cookieNamed(rec, sessionCookie) != nil {
				t.Fatal("session cookie set on failure")
			}
		})
	}
}

func TestCallbackNotAllowed(t *testing.T) {
	cases := map[string]func(c map[string]any){
		"unverified email": func(c map[string]any) { c["email_verified"] = false },
		"other domain":     func(c map[string]any) { c["hd"] = "gmail.com" },
		"no hd claim":      func(c map[string]any) { delete(c, "hd") },
		"not on allowlist": func(c map[string]any) { c["email"] = "eve@percona.com" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			hs := newHarness(t, "alice@percona.com")
			st, nn := hs.startLogin()
			claims := validClaims("alice@percona.com", nn.Value)
			mutate(claims)
			rec := hs.callback("code=c&state="+st.Value, claims, st, nn)
			loc := rec.Header().Get("Location")
			if rec.Code != http.StatusFound || !strings.HasPrefix(loc, "/?auth_error=not_allowed&email=") {
				t.Fatalf("code=%d location=%q", rec.Code, loc)
			}
			if cookieNamed(rec, sessionCookie) != nil {
				t.Fatal("session cookie set for refused user")
			}
		})
	}
}

func TestProtectedRoute(t *testing.T) {
	hs := newHarness(t, "alice@percona.com")
	if rec := hs.post("/api/protected", testOrigin); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d", rec.Code)
	}
	sess := hs.signIn("alice@percona.com")
	rec := hs.post("/api/protected", testOrigin, sess)
	if rec.Code != http.StatusNoContent || rec.Header().Get("X-User") != "alice@percona.com" {
		t.Fatalf("signed in: %d user=%q", rec.Code, rec.Header().Get("X-User"))
	}
	if rec := hs.post("/api/protected", "http://evil.test", sess); rec.Code != http.StatusForbidden {
		t.Fatalf("bad origin: %d", rec.Code)
	}
	if rec := hs.post("/api/protected", "", sess); rec.Code != http.StatusForbidden {
		t.Fatalf("missing origin: %d", rec.Code)
	}
}

// Review focus 2.
func TestExpiredSessionIsAnonymous(t *testing.T) {
	hs := newHarness(t, "alice@percona.com")
	sess := hs.signIn("alice@percona.com")
	hs.clock = hs.clock.Add(time.Hour)
	if rec := hs.post("/api/protected", testOrigin, sess); rec.Code != http.StatusUnauthorized {
		t.Fatalf("expired session: %d", rec.Code)
	}
}

func TestLogout(t *testing.T) {
	hs := newHarness(t, "alice@percona.com")
	sess := hs.signIn("alice@percona.com")
	rec := hs.post("/auth/logout", testOrigin, sess)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("logout: %d", rec.Code)
	}
	if c := cookieNamed(rec, sessionCookie); c == nil || c.MaxAge >= 0 {
		t.Fatalf("cookie not cleared: %+v", c)
	}
	if rec := hs.post("/api/protected", testOrigin, sess); rec.Code != http.StatusUnauthorized {
		t.Fatalf("after logout: %d", rec.Code)
	}
}

func TestDisabledService(t *testing.T) {
	db := newTestDB(t)
	svc := NewService(db, Options{Admins: []string{"alice@percona.com"}})
	h := testRouter(svc)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/auth/login", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("login when disabled: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/me", nil))
	if email, enabled := me(t, rec); email != nil || enabled {
		t.Fatalf("me when disabled: %v %v", email, enabled)
	}
	sid, err := NewStore(db).CreateSession("alice@percona.com", time.Now(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/protected", nil)
	req.Header.Set("Origin", testOrigin)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: sid})
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("protected when disabled: %d", rec.Code)
	}
}
```

- [ ] **Step 2: Run to verify failure.** `cd backend && go test ./internal/auth/ -v` → FAIL to compile (`undefined: NewService`).

- [ ] **Step 3: Implement** `backend/internal/auth/service.go`:

```go
package auth

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

const (
	sessionCookie = "obsboard_session"
	stateCookie   = "obsboard_oauth_state"
	nonceCookie   = "obsboard_oauth_nonce"
)

// Options configures a Service. A nil Provider disables sign-in.
type Options struct {
	Provider   *Provider
	PublicURL  string
	Admins     []string
	SessionTTL time.Duration
	Now        func() time.Time
}

// Service owns sign-in, sessions and the allowlist.
type Service struct {
	store     *Store
	provider  *Provider
	publicURL string
	secure    bool
	admins    map[string]bool
	adminList []string
	ttl       time.Duration
	now       func() time.Time
}

func NewService(db *sql.DB, o Options) *Service {
	s := &Service{
		store:     NewStore(db),
		provider:  o.Provider,
		publicURL: strings.TrimRight(o.PublicURL, "/"),
		secure:    strings.HasPrefix(o.PublicURL, "https://"),
		admins:    map[string]bool{},
		adminList: []string{},
		ttl:       o.SessionTTL,
		now:       o.Now,
	}
	if s.ttl <= 0 {
		s.ttl = 168 * time.Hour
	}
	if s.now == nil {
		s.now = time.Now
	}
	for _, a := range o.Admins {
		a = normalizeEmail(a)
		if a != "" && !s.admins[a] {
			s.admins[a] = true
			s.adminList = append(s.adminList, a)
		}
	}
	return s
}

func normalizeEmail(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// Enabled reports whether Google sign-in is configured.
func (s *Service) Enabled() bool { return s.provider != nil }

// IsAllowed reports whether email is a config admin or on the allowlist.
func (s *Service) IsAllowed(email string) (bool, error) {
	if s.admins[email] {
		return true, nil
	}
	return s.store.HasUser(email)
}

// PruneExpired deletes expired sessions; run once at startup.
func (s *Service) PruneExpired() error {
	_, err := s.store.PruneExpired(s.now())
	return err
}

type ctxKey struct{}

// UserFrom returns the signed-in email set by LoadUser.
func UserFrom(ctx context.Context) (string, bool) {
	e, ok := ctx.Value(ctxKey{}).(string)
	return e, ok
}

// LoadUser resolves the session cookie, if any, to an allowed email. Requests
// without a valid session pass through anonymously.
func (s *Service) LoadUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if !s.Enabled() || err != nil || c.Value == "" {
			next.ServeHTTP(w, r)
			return
		}
		email, ok, err := s.store.SessionEmail(c.Value, s.now())
		if err != nil {
			slog.Warn("auth: session lookup failed", "err", err)
		}
		if ok {
			allowed, err := s.IsAllowed(email)
			if err != nil {
				slog.Warn("auth: allowlist lookup failed", "err", err)
			} else if allowed {
				r = r.WithContext(context.WithValue(r.Context(), ctxKey{}, email))
			}
		}
		next.ServeHTTP(w, r)
	})
}

// RequireUser rejects requests without a signed-in user.
func (s *Service) RequireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := UserFrom(r.Context()); !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// CheckOrigin rejects state-changing requests whose Origin is not public_url.
func (s *Service) CheckOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			if r.Header.Get("Origin") != s.publicURL {
				http.Error(w, "forbidden: bad origin", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// Mount registers the auth routes. /auth/* exists only when sign-in is
// enabled; /api/me always exists.
func (s *Service) Mount(r chi.Router) {
	r.Get("/api/me", s.handleMe)
	if !s.Enabled() {
		return
	}
	r.Get("/auth/login", s.handleLogin)
	r.Get("/auth/callback", s.handleCallback)
	r.With(s.RequireUser, s.CheckOrigin).Post("/auth/logout", s.handleLogout)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Service) handleMe(w http.ResponseWriter, r *http.Request) {
	resp := struct {
		Email       *string `json:"email"`
		AuthEnabled bool    `json:"auth_enabled"`
	}{AuthEnabled: s.Enabled()}
	if e, ok := UserFrom(r.Context()); ok {
		resp.Email = &e
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Service) setCookie(w http.ResponseWriter, name, value, path string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: value, Path: path, MaxAge: maxAge,
		HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteLaxMode,
	})
}

func (s *Service) handleLogin(w http.ResponseWriter, r *http.Request) {
	state, err := randomToken()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	nonce, err := randomToken()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.setCookie(w, stateCookie, state, "/auth", 600)
	s.setCookie(w, nonceCookie, nonce, "/auth", 600)
	http.Redirect(w, r, s.provider.AuthCodeURL(state, nonce), http.StatusFound)
}

func equalToken(a, b string) bool {
	return a != "" && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func (s *Service) handleCallback(w http.ResponseWriter, r *http.Request) {
	fail := func(reason string, err error) {
		slog.Warn("auth: sign-in failed", "reason", reason, "err", err)
		http.Redirect(w, r, "/?auth_error=failed", http.StatusFound)
	}
	q := r.URL.Query()
	stateC, stateErr := r.Cookie(stateCookie)
	nonceC, nonceErr := r.Cookie(nonceCookie)
	// The state and nonce are single-use whatever the outcome.
	s.setCookie(w, stateCookie, "", "/auth", -1)
	s.setCookie(w, nonceCookie, "", "/auth", -1)

	if e := q.Get("error"); e != "" {
		fail("provider returned "+e, nil)
		return
	}
	if stateErr != nil || nonceErr != nil || !equalToken(stateC.Value, q.Get("state")) {
		fail("state mismatch", nil)
		return
	}
	id, err := s.provider.Exchange(r.Context(), q.Get("code"))
	if err != nil {
		fail("exchange", err)
		return
	}
	if !equalToken(nonceC.Value, id.Nonce) {
		fail("nonce mismatch", nil)
		return
	}
	allowed := id.EmailVerified && id.HostedDomain == AllowedDomain && id.Email != ""
	if allowed {
		if allowed, err = s.IsAllowed(id.Email); err != nil {
			fail("allowlist lookup", err)
			return
		}
	}
	if !allowed {
		slog.Info("auth: sign-in refused", "email", id.Email, "verified", id.EmailVerified, "hd", id.HostedDomain)
		http.Redirect(w, r, "/?auth_error=not_allowed&email="+url.QueryEscape(id.Email), http.StatusFound)
		return
	}
	sid, err := s.store.CreateSession(id.Email, s.now(), s.ttl)
	if err != nil {
		fail("create session", err)
		return
	}
	s.setCookie(w, sessionCookie, sid, "/", int(s.ttl.Seconds()))
	slog.Info("auth: signed in", "email", id.Email)
	http.Redirect(w, r, "/", http.StatusFound)
}

func (s *Service) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		if err := s.store.DeleteSession(c.Value); err != nil {
			slog.Warn("auth: delete session failed", "err", err)
		}
	}
	s.setCookie(w, sessionCookie, "", "/", -1)
	w.WriteHeader(http.StatusNoContent)
}
```

- [ ] **Step 4: Run tests.** `cd backend && go test ./internal/auth/ -v` → PASS.

- [ ] **Step 5: Commit.**

```bash
git add backend/internal/auth/service.go backend/internal/auth/service_test.go
git commit -s -m "feat(auth): session middleware and Google sign-in handlers"
```

```json:metadata
{"files": ["backend/internal/auth/service.go", "backend/internal/auth/service_test.go"], "verifyCommand": "cd backend && go test ./internal/auth/ -v", "acceptanceCriteria": ["/auth/login sets state+nonce cookies (HttpOnly, Path /auth, Max-Age 600) and 302s with state and hd", "callback success sets obsboard_session (HttpOnly, Path /, Lax) and 302s to /", "provider error, state mismatch, missing state cookie, nonce mismatch, failed exchange -> /?auth_error=failed", "unverified, wrong hd, not allowed -> /?auth_error=not_allowed&email=", "protected: 401 anonymous, passes with session+Origin, 403 bad or missing Origin", "expired session anonymous; logout deletes session, clears cookie, 204", "disabled: /auth/login 404, /api/me auth_enabled false, protected 401"], "modelTier": "standard"}
```

---

### Task 5: Allowlist management endpoints

**Goal:** `GET/POST /api/settings/users` and `DELETE /api/settings/users/{email}` behind `RequireUser` + `CheckOrigin`.

**Files:**
- Create: `backend/internal/auth/users.go`
- Modify: `backend/internal/auth/service.go` (`Mount`)
- Test: `backend/internal/auth/users_test.go`

**Acceptance Criteria:**
- [ ] GET returns `{"admins":[...],"users":[{"email","added_by","added_at"}]}` (admins in config order, users by email).
- [ ] POST normalizes the email; 400 unless `local@percona.com`; 409 for a config admin or an existing user; 201 with the row and `added_by` = caller.
- [ ] DELETE: 400 for a config admin or the caller; 404 if absent; 204 otherwise; the path email is decoded and normalized.
- [ ] A removed user's existing session gets 401 on the next protected request.
- [ ] All three return 401 without a session; POST/DELETE return 403 with a bad Origin.

**Verify:** `cd backend && go test ./internal/auth/ -run Users\|Removed\|RemoveUser -v` → all PASS

**Steps:**

- [ ] **Step 1: Write the failing tests** `backend/internal/auth/users_test.go`:

```go
package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func (hs *harness) send(method, path, body, origin string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	return hs.do(req, cookies...)
}

type usersBody struct {
	Admins []string `json:"admins"`
	Users  []User   `json:"users"`
}

func listUsers(t *testing.T, hs *harness, sess *http.Cookie) usersBody {
	t.Helper()
	rec := hs.send(http.MethodGet, "/api/settings/users", "", "", sess)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	var b usersBody
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestUsersAddAndList(t *testing.T) {
	hs := newHarness(t, "alice@percona.com", "root@percona.com")
	sess := hs.signIn("alice@percona.com")
	rec := hs.send(http.MethodPost, "/api/settings/users", `{"email":"  Bob@Percona.com "}`, testOrigin, sess)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add: %d %s", rec.Code, rec.Body.String())
	}
	var u User
	if err := json.Unmarshal(rec.Body.Bytes(), &u); err != nil {
		t.Fatal(err)
	}
	if u.Email != "bob@percona.com" || u.AddedBy != "alice@percona.com" || u.AddedAt == "" {
		t.Fatalf("row: %+v", u)
	}
	b := listUsers(t, hs, sess)
	if strings.Join(b.Admins, ",") != "alice@percona.com,root@percona.com" || len(b.Users) != 1 || b.Users[0].Email != "bob@percona.com" {
		t.Fatalf("list: %+v", b)
	}
}

func TestUsersAddRejects(t *testing.T) {
	hs := newHarness(t, "alice@percona.com")
	sess := hs.signIn("alice@percona.com")
	for body, want := range map[string]int{
		`not json`:                    http.StatusBadRequest,
		`{"email":"bob@gmail.com"}`:   http.StatusBadRequest,
		`{"email":"@percona.com"}`:    http.StatusBadRequest,
		`{"email":"a@b@percona.com"}`: http.StatusBadRequest,
		`{"email":"ALICE@percona.com"}`: http.StatusConflict,
	} {
		if rec := hs.send(http.MethodPost, "/api/settings/users", body, testOrigin, sess); rec.Code != want {
			t.Errorf("%s: got %d want %d (%s)", body, rec.Code, want, rec.Body.String())
		}
	}
	if rec := hs.send(http.MethodPost, "/api/settings/users", `{"email":"bob@percona.com"}`, testOrigin, sess); rec.Code != http.StatusCreated {
		t.Fatalf("first add: %d", rec.Code)
	}
	if rec := hs.send(http.MethodPost, "/api/settings/users", `{"email":"bob@percona.com"}`, testOrigin, sess); rec.Code != http.StatusConflict {
		t.Fatalf("duplicate add: %d", rec.Code)
	}
}

func TestUsersRemove(t *testing.T) {
	hs := newHarness(t, "alice@percona.com")
	if _, err := hs.svc.store.AddUser("bob@percona.com", "alice@percona.com", t0); err != nil {
		t.Fatal(err)
	}
	if _, err := hs.svc.store.AddUser("carol@percona.com", "alice@percona.com", t0); err != nil {
		t.Fatal(err)
	}
	carol := hs.signIn("carol@percona.com")
	for path, want := range map[string]int{
		"/api/settings/users/alice@percona.com": http.StatusBadRequest, // config admin
		"/api/settings/users/carol@percona.com": http.StatusBadRequest, // self
		"/api/settings/users/nobody@percona.com": http.StatusNotFound,
		"/api/settings/users/bob@percona.com":   http.StatusNoContent,
	} {
		if rec := hs.send(http.MethodDelete, path, "", testOrigin, carol); rec.Code != want {
			t.Errorf("%s: got %d want %d (%s)", path, rec.Code, want, rec.Body.String())
		}
	}
	if b := listUsers(t, hs, carol); len(b.Users) != 1 || b.Users[0].Email != "carol@percona.com" {
		t.Fatalf("after remove: %+v", b.Users)
	}
}

// Review focus 5.
func TestRemoveUserNormalizesPath(t *testing.T) {
	hs := newHarness(t, "alice@percona.com")
	if _, err := hs.svc.store.AddUser("bob@percona.com", "alice@percona.com", t0); err != nil {
		t.Fatal(err)
	}
	sess := hs.signIn("alice@percona.com")
	if rec := hs.send(http.MethodDelete, "/api/settings/users/Bob%40Percona.com", "", testOrigin, sess); rec.Code != http.StatusNoContent {
		t.Fatalf("encoded delete: %d %s", rec.Code, rec.Body.String())
	}
}

// Review focus 3.
func TestRemovedUserLosesAccess(t *testing.T) {
	hs := newHarness(t, "alice@percona.com")
	if _, err := hs.svc.store.AddUser("bob@percona.com", "alice@percona.com", t0); err != nil {
		t.Fatal(err)
	}
	bob := hs.signIn("bob@percona.com")
	alice := hs.signIn("alice@percona.com")
	if rec := hs.post("/api/protected", testOrigin, bob); rec.Code != http.StatusNoContent {
		t.Fatalf("bob before removal: %d", rec.Code)
	}
	if rec := hs.send(http.MethodDelete, "/api/settings/users/bob@percona.com", "", testOrigin, alice); rec.Code != http.StatusNoContent {
		t.Fatalf("remove bob: %d", rec.Code)
	}
	if rec := hs.post("/api/protected", testOrigin, bob); rec.Code != http.StatusUnauthorized {
		t.Fatalf("bob after removal: %d", rec.Code)
	}
}

func TestUsersRequireSessionAndOrigin(t *testing.T) {
	hs := newHarness(t, "alice@percona.com")
	if rec := hs.send(http.MethodGet, "/api/settings/users", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous list: %d", rec.Code)
	}
	if rec := hs.send(http.MethodPost, "/api/settings/users", `{"email":"bob@percona.com"}`, testOrigin); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous add: %d", rec.Code)
	}
	sess := hs.signIn("alice@percona.com")
	if rec := hs.send(http.MethodPost, "/api/settings/users", `{"email":"bob@percona.com"}`, "http://evil.test", sess); rec.Code != http.StatusForbidden {
		t.Fatalf("bad origin add: %d", rec.Code)
	}
	if rec := hs.send(http.MethodDelete, "/api/settings/users/bob@percona.com", "", "http://evil.test", sess); rec.Code != http.StatusForbidden {
		t.Fatalf("bad origin delete: %d", rec.Code)
	}
}
```

- [ ] **Step 2: Run to verify failure.** `cd backend && go test ./internal/auth/ -run Users -v` → FAIL (404 from unregistered routes).

- [ ] **Step 3: Implement** `backend/internal/auth/users.go`:

```go
package auth

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
)

// validDomainEmail reports whether e is local@percona.com with a plain local part.
func validDomainEmail(e string) bool {
	local, domain, ok := strings.Cut(e, "@")
	return ok && local != "" && domain == AllowedDomain && !strings.ContainsAny(local, "@ \t")
}

func (s *Service) handleListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.store.ListUsers()
	if err != nil {
		slog.Error("auth: list users", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"admins": s.adminList, "users": users})
}

func (s *Service) handleAddUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	email := normalizeEmail(body.Email)
	if !validDomainEmail(email) {
		http.Error(w, "email must be a @"+AllowedDomain+" address", http.StatusBadRequest)
		return
	}
	if s.admins[email] {
		http.Error(w, "already an admin", http.StatusConflict)
		return
	}
	caller, _ := UserFrom(r.Context())
	u, err := s.store.AddUser(email, caller, s.now())
	if errors.Is(err, ErrExists) {
		http.Error(w, "already allowed", http.StatusConflict)
		return
	}
	if err != nil {
		slog.Error("auth: add user", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	slog.Info("auth: user added", "email", email, "by", caller)
	writeJSON(w, http.StatusCreated, u)
}

func (s *Service) handleRemoveUser(w http.ResponseWriter, r *http.Request) {
	raw, err := url.PathUnescape(chi.URLParam(r, "email"))
	if err != nil {
		http.Error(w, "invalid email", http.StatusBadRequest)
		return
	}
	email := normalizeEmail(raw)
	caller, _ := UserFrom(r.Context())
	if s.admins[email] {
		http.Error(w, "config admins cannot be removed", http.StatusBadRequest)
		return
	}
	if email == caller {
		http.Error(w, "you cannot remove yourself", http.StatusBadRequest)
		return
	}
	err = s.store.RemoveUser(email)
	if errors.Is(err, ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		slog.Error("auth: remove user", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	slog.Info("auth: user removed", "email", email, "by", caller)
	w.WriteHeader(http.StatusNoContent)
}
```

In `service.go`, change `Mount` so the settings routes are always registered (they answer 401 when sign-in is disabled because no user is ever loaded):

```go
func (s *Service) Mount(r chi.Router) {
	r.Get("/api/me", s.handleMe)
	r.Group(func(r chi.Router) {
		r.Use(s.RequireUser, s.CheckOrigin)
		r.Get("/api/settings/users", s.handleListUsers)
		r.Post("/api/settings/users", s.handleAddUser)
		r.Delete("/api/settings/users/{email}", s.handleRemoveUser)
	})
	if !s.Enabled() {
		return
	}
	r.Get("/auth/login", s.handleLogin)
	r.Get("/auth/callback", s.handleCallback)
	r.With(s.RequireUser, s.CheckOrigin).Post("/auth/logout", s.handleLogout)
}
```

- [ ] **Step 4: Run tests.** `cd backend && go test ./internal/auth/ -v` → PASS (all auth tests).

- [ ] **Step 5: Commit.**

```bash
git add backend/internal/auth/users.go backend/internal/auth/users_test.go backend/internal/auth/service.go
git commit -s -m "feat(auth): allowlist management endpoints"
```

```json:metadata
{"files": ["backend/internal/auth/users.go", "backend/internal/auth/service.go", "backend/internal/auth/users_test.go"], "verifyCommand": "cd backend && go test ./internal/auth/ -v", "acceptanceCriteria": ["GET returns admins in config order and users by email", "POST normalizes; 400 non-percona/malformed; 409 admin or existing; 201 with added_by=caller", "DELETE 400 admin or self, 404 absent, 204 otherwise; encoded mixed-case path normalized", "removed user's session gets 401 on next protected request", "401 without session; 403 bad Origin on POST/DELETE"], "modelTier": "mechanical"}
```

---

### Task 6: Wire auth into the router and server

**Goal:** `api.NewRouter` takes `*auth.Service`, loads users on every request, mounts the auth routes, and puts `POST /api/rebuild` and `POST /api/telemetry` behind `RequireUser` + `CheckOrigin`; `main.go` builds the service and routes `/auth/*` to the router.

**Files:**
- Modify: `backend/internal/api/server.go`
- Modify: `backend/internal/api/handlers_test.go` (3 `NewRouter` call sites: lines ~43, ~276, ~617)
- Modify: `backend/cmd/obsboard/main.go`
- Test: `backend/internal/api/auth_routes_test.go`

**Acceptance Criteria:**
- [ ] With auth disabled: `POST /api/rebuild` and `POST /api/telemetry` → 401; `GET /api/telemetry`, `GET /api/me`, `POST /api/presence` do not return 401.
- [ ] With a valid session and `Origin` = public_url: `POST /api/telemetry?enabled=true` → 200 and the flag is set; without Origin → 403.
- [ ] `main.go`: Google discovery failure aborts startup when `GOOGLE_CLIENT_ID` is set; disabled mode logs once; expired sessions pruned at startup; requests under `/auth/` reach the router when `FRONTEND_DIR` is set.
- [ ] `go build ./...` and `go test ./...` pass.

**Verify:** `cd backend && go build ./... && go test ./...` → ok for every package

**Steps:**

- [ ] **Step 1: Write the failing tests** `backend/internal/api/auth_routes_test.go`:

```go
package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/percona/obs-dashboard/internal/auth"
	"github.com/percona/obs-dashboard/internal/hub"
	"github.com/percona/obs-dashboard/internal/obs"
	"github.com/percona/obs-dashboard/internal/store"
	"golang.org/x/oauth2"
)

const dashOrigin = "http://dash.test"

func authRouter(t *testing.T, enabled bool) (http.Handler, *atomic.Bool, string) {
	t.Helper()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	opts := auth.Options{PublicURL: dashOrigin, Admins: []string{"alice@percona.com"}}
	sid := ""
	if enabled {
		// A provider that is never called: sessions are created directly.
		opts.Provider = auth.NewProvider(oauth2.Config{}, nil)
		if sid, err = auth.NewStore(db).CreateSession("alice@percona.com", time.Now(), time.Hour); err != nil {
			t.Fatal(err)
		}
	}
	obsClient := obs.SingleFleet(obs.NewClient(stubOBSServer(t).URL, "user", "pass"), "isv:percona")
	tel := new(atomic.Bool)
	r := NewRouter(db, hub.New(), obsClient, fakeStatter{}, tel, time.Duration(0), fakePollState{s: "active"}, auth.NewService(db, opts))
	return r, tel, sid
}

func call(h http.Handler, method, path, body, origin, sid string) int {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if sid != "" {
		req.AddCookie(&http.Cookie{Name: "obsboard_session", Value: sid})
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

func TestActionsRequireAuthWhenDisabled(t *testing.T) {
	h, _, _ := authRouter(t, false)
	rebuild := `{"project":"ppg:staging:18","repo":"r","arch":"x86_64","package":"p"}`
	if code := call(h, http.MethodPost, "/api/rebuild", rebuild, dashOrigin, ""); code != http.StatusUnauthorized {
		t.Errorf("rebuild: %d", code)
	}
	if code := call(h, http.MethodPost, "/api/telemetry?enabled=true", "", dashOrigin, ""); code != http.StatusUnauthorized {
		t.Errorf("telemetry set: %d", code)
	}
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/telemetry"},
		{http.MethodGet, "/api/me"},
		{http.MethodPost, "/api/presence"},
	} {
		if code := call(h, c.method, c.path, "", "", ""); code == http.StatusUnauthorized {
			t.Errorf("%s %s should stay public, got 401", c.method, c.path)
		}
	}
}

func TestTelemetryWithSession(t *testing.T) {
	h, tel, sid := authRouter(t, true)
	if code := call(h, http.MethodPost, "/api/telemetry?enabled=true", "", "", sid); code != http.StatusForbidden {
		t.Fatalf("missing origin: %d", code)
	}
	if code := call(h, http.MethodPost, "/api/telemetry?enabled=true", "", dashOrigin, sid); code != http.StatusOK {
		t.Fatalf("with session: %d", code)
	}
	if !tel.Load() {
		t.Fatal("telemetry flag not set")
	}
}
```

- [ ] **Step 2: Run to verify failure.** `cd backend && go test ./internal/api/ -run 'Actions|TelemetryWith' -v` → FAIL to compile (too many arguments to `NewRouter`).

- [ ] **Step 3: Implement the router change** in `backend/internal/api/server.go`. Add the import `"github.com/percona/obs-dashboard/internal/auth"`, change the signature and wiring:

```go
// NewRouter creates the chi router with all API routes registered. authSvc
// gates state-changing routes; reads stay public.
func NewRouter(db *sql.DB, h *hub.Hub, fleet *obs.Fleet, ws Statter, telemetryEnabled *atomic.Bool, telemetryInterval time.Duration, gate PresenceGate, authSvc *auth.Service) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	// (existing obs.Interactive middleware unchanged)
	r.Use(authSvc.LoadUser)
	authSvc.Mount(r)
```

Delete the two lines `r.Post("/api/rebuild", rebuildHandler(fleet))` and `r.Post("/api/telemetry", telemetrySetHandler(telemetryEnabled))`, and add before `return r`:

```go
	// State-changing actions: signed-in, allowlisted users only.
	r.Group(func(r chi.Router) {
		r.Use(authSvc.RequireUser, authSvc.CheckOrigin)
		r.Post("/api/rebuild", rebuildHandler(fleet))
		r.Post("/api/telemetry", telemetrySetHandler(telemetryEnabled))
	})
```

- [ ] **Step 4: Update existing test call sites.** In `backend/internal/api/handlers_test.go`, each of the three `NewRouter(...)` calls gets a final argument `auth.NewService(db, auth.Options{})` (disabled), and the import `"github.com/percona/obs-dashboard/internal/auth"` is added. None of those tests POST to `/api/rebuild` or `/api/telemetry` through the router (rebuild tests call `rebuildHandler` directly), so they keep passing.

- [ ] **Step 5: Wire `main.go`.** In `backend/cmd/obsboard/main.go`, add import `"github.com/percona/obs-dashboard/internal/auth"` (and `"strings"` if missing). Replace the line `router := api.NewRouter(db, h, fleet, ws, telemetryEnabled, cfg.Telemetry.Interval, gate)` with:

```go
	var provider *auth.Provider
	if cfg.Auth.Enabled() {
		provider, err = auth.NewGoogleProvider(ctx, cfg.Auth.GoogleClientID, cfg.Auth.GoogleClientSecret, cfg.Auth.PublicURL+"/auth/callback")
		if err != nil {
			return fmt.Errorf("auth: %w", err)
		}
		slog.Info("auth: Google sign-in enabled", "public_url", cfg.Auth.PublicURL, "admins", len(cfg.Auth.Admins))
	} else {
		slog.Info("auth: GOOGLE_CLIENT_ID not set; sign-in disabled and actions unavailable")
	}
	authSvc := auth.NewService(db, auth.Options{
		Provider:   provider,
		PublicURL:  cfg.Auth.PublicURL,
		Admins:     cfg.Auth.Admins,
		SessionTTL: cfg.Auth.SessionTTL,
	})
	if err := authSvc.PruneExpired(); err != nil {
		slog.Warn("auth: prune expired sessions", "err", err)
	}

	router := api.NewRouter(db, h, fleet, ws, telemetryEnabled, cfg.Telemetry.Interval, gate, authSvc)
```

In the `FRONTEND_DIR` dispatch, replace the condition `if len(r.URL.Path) >= 4 && r.URL.Path[:4] == "/api" {` with:

```go
			if strings.HasPrefix(r.URL.Path, "/api") || strings.HasPrefix(r.URL.Path, "/auth/") {
```

- [ ] **Step 6: Run everything.** `cd backend && go vet ./... && go build ./... && go test ./...` → all ok.

- [ ] **Step 7: Commit.**

```bash
git add backend/internal/api/server.go backend/internal/api/handlers_test.go backend/internal/api/auth_routes_test.go backend/cmd/obsboard/main.go
git commit -s -m "feat(api): require sign-in for rebuild and telemetry actions"
```

```json:metadata
{"files": ["backend/internal/api/server.go", "backend/internal/api/handlers_test.go", "backend/internal/api/auth_routes_test.go", "backend/cmd/obsboard/main.go"], "verifyCommand": "cd backend && go vet ./... && go build ./... && go test ./...", "acceptanceCriteria": ["auth disabled: POST /api/rebuild and /api/telemetry 401; GET /api/telemetry, /api/me, POST /api/presence not 401", "session + Origin: POST /api/telemetry?enabled=true 200 and flag set; no Origin 403", "main: discovery failure aborts startup when enabled; disabled logs once; prune at startup; /auth/ routed to router with FRONTEND_DIR", "go build ./... and go test ./... pass"], "modelTier": "standard"}
```

---

### Task 7: Frontend auth state

**Goal:** A module-level `useAuth` composable (session state, notice, login/logout, 401 handling, settings-tab guard), the `MainTab` type with `'settings'`, and the `/auth` dev proxy.

**Files:**
- Create: `frontend/src/composables/useAuth.ts`
- Modify: `frontend/src/composables/useUrlState.ts`
- Modify: `frontend/vite.config.ts`
- Modify: `frontend/src/App.vue` (one line: the `mainTab` ref type)
- Test: `frontend/src/composables/useAuth.test.ts`

**Acceptance Criteria:**
- [ ] `loadAuth()` sets `user`/`authEnabled` from `GET /api/me` and `loaded=true` even when the request fails.
- [ ] `handleUnauthorized()` clears `user` and shows "Session expired, sign in again" only if someone was signed in.
- [ ] `consumeAuthError()` turns `?auth_error=not_allowed&email=x` into "x is not allowed to sign in" (`failed` → "Sign-in failed") and removes both params from the URL, keeping others.
- [ ] `guardSettingsTab(tab)` switches `'settings'` to `'overview'` once loaded and signed out; leaves it while not loaded or signed in.
- [ ] `useUrlState` accepts `?tab=settings`; `MainTab` exported from `useUrlState.ts`.
- [ ] Vite dev server proxies `/auth` to the backend.

**Verify:** `cd frontend && npx vitest run src/composables/useAuth.test.ts` → all PASS

**Steps:**

- [ ] **Step 1: Write the failing tests** `frontend/src/composables/useAuth.test.ts`:

```ts
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { nextTick, ref } from 'vue'
import {
  useAuth, loadAuth, handleUnauthorized, consumeAuthError, guardSettingsTab, logout, resetAuthForTests,
} from './useAuth'
import type { MainTab } from './useUrlState'

function stubFetch(impl: (url: string, init?: RequestInit) => Promise<Response>) {
  const fn = vi.fn(impl)
  vi.stubGlobal('fetch', fn)
  return fn
}
const json = (body: unknown, status = 200) =>
  Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))

beforeEach(() => resetAuthForTests())
afterEach(() => { vi.unstubAllGlobals(); vi.useRealTimers() })

describe('useAuth', () => {
  it('loads the signed-in user', async () => {
    stubFetch(() => json({ email: 'alice@percona.com', auth_enabled: true }))
    await loadAuth()
    const { user, authEnabled, signedIn, loaded } = useAuth()
    expect(user.value).toBe('alice@percona.com')
    expect(authEnabled.value).toBe(true)
    expect(signedIn.value).toBe(true)
    expect(loaded.value).toBe(true)
  })

  it('stays signed out but loaded when /api/me fails', async () => {
    stubFetch(() => Promise.reject(new Error('down')))
    await loadAuth()
    const { signedIn, loaded } = useAuth()
    expect(signedIn.value).toBe(false)
    expect(loaded.value).toBe(true)
  })

  it('handleUnauthorized clears the user and explains why', () => {
    const { user, notice } = useAuth()
    user.value = 'alice@percona.com'
    handleUnauthorized()
    expect(user.value).toBeNull()
    expect(notice.value).toBe('Session expired, sign in again')
  })

  it('handleUnauthorized is silent when nobody was signed in', () => {
    handleUnauthorized()
    expect(useAuth().notice.value).toBeNull()
  })

  it('logout posts and clears the user', async () => {
    const fetchFn = stubFetch(() => Promise.resolve(new Response(null, { status: 204 })))
    const { user } = useAuth()
    user.value = 'alice@percona.com'
    await logout()
    expect(fetchFn).toHaveBeenCalledWith('/auth/logout', { method: 'POST' })
    expect(user.value).toBeNull()
  })

  it('consumeAuthError shows the refused email and cleans the URL', () => {
    window.history.replaceState(null, '', '/?tab=board&auth_error=not_allowed&email=eve%40percona.com')
    consumeAuthError()
    expect(useAuth().notice.value).toBe('eve@percona.com is not allowed to sign in')
    expect(window.location.search).toBe('?tab=board')
  })

  it('consumeAuthError reports a generic failure', () => {
    window.history.replaceState(null, '', '/?auth_error=failed')
    consumeAuthError()
    expect(useAuth().notice.value).toBe('Sign-in failed')
    expect(window.location.search).toBe('')
  })

  it('notice clears itself after 6 seconds', () => {
    vi.useFakeTimers()
    const { user, notice } = useAuth()
    user.value = 'a@percona.com'
    handleUnauthorized()
    vi.advanceTimersByTime(6000)
    expect(notice.value).toBeNull()
  })

  it('guardSettingsTab sends signed-out visitors to overview once loaded', async () => {
    const tab = ref<MainTab>('settings')
    guardSettingsTab(tab)
    expect(tab.value).toBe('settings') // not loaded yet
    stubFetch(() => json({ email: null, auth_enabled: true }))
    await loadAuth()
    await nextTick()
    expect(tab.value).toBe('overview')
  })

  it('guardSettingsTab keeps settings for a signed-in user', async () => {
    const tab = ref<MainTab>('settings')
    guardSettingsTab(tab)
    stubFetch(() => json({ email: 'alice@percona.com', auth_enabled: true }))
    await loadAuth()
    await nextTick()
    expect(tab.value).toBe('settings')
  })
})
```

- [ ] **Step 2: Run to verify failure.** `cd frontend && npx vitest run src/composables/useAuth.test.ts` → FAIL (cannot resolve `./useAuth`).

- [ ] **Step 3: Add `MainTab`** in `frontend/src/composables/useUrlState.ts`: add `export type MainTab = 'board' | 'artifacts' | 'overview' | 'settings'` after the imports; change `mainTab: Ref<'board' | 'artifacts' | 'overview'>` in `UrlStateOptions` to `mainTab: Ref<MainTab>`; change the hydration line to:

```ts
    if (tab === 'board' || tab === 'artifacts' || tab === 'overview' || tab === 'settings') mainTab.value = tab
```

- [ ] **Step 4: Implement** `frontend/src/composables/useAuth.ts`:

```ts
import { computed, ref, watch, type Ref } from 'vue'
import type { MainTab } from './useUrlState'

// Module-level singleton: one session per page.
const user = ref<string | null>(null)
const authEnabled = ref(false)
const loaded = ref(false)
const notice = ref<string | null>(null)
const signedIn = computed(() => user.value !== null)
let noticeTimer: ReturnType<typeof setTimeout> | undefined

export function showNotice(text: string): void {
  notice.value = text
  if (noticeTimer !== undefined) clearTimeout(noticeTimer)
  noticeTimer = setTimeout(() => {
    notice.value = null
    noticeTimer = undefined
  }, 6000)
}

export function dismissNotice(): void {
  if (noticeTimer !== undefined) clearTimeout(noticeTimer)
  noticeTimer = undefined
  notice.value = null
}

export async function loadAuth(): Promise<void> {
  try {
    const res = await fetch('/api/me')
    if (res.ok) {
      const body = await res.json() as { email: string | null; auth_enabled: boolean }
      user.value = body.email
      authEnabled.value = body.auth_enabled
    }
  } catch {
    // Backend unreachable: stay signed out.
  } finally {
    loaded.value = true
  }
}

// consumeAuthError reports a failed sign-in redirect (?auth_error=...) once
// and removes its parameters from the address bar.
export function consumeAuthError(): void {
  const url = new URL(window.location.href)
  const code = url.searchParams.get('auth_error')
  if (!code) return
  const email = url.searchParams.get('email')
  if (code === 'not_allowed') {
    showNotice(email ? `${email} is not allowed to sign in` : 'This account is not allowed to sign in')
  } else {
    showNotice('Sign-in failed')
  }
  url.searchParams.delete('auth_error')
  url.searchParams.delete('email')
  window.history.replaceState(window.history.state, '', url.pathname + url.search + url.hash)
}

export function login(): void {
  window.location.assign('/auth/login')
}

export async function logout(): Promise<void> {
  try {
    await fetch('/auth/logout', { method: 'POST' })
  } catch {
    // The session cookie may survive; the UI still signs out.
  }
  user.value = null
}

// handleUnauthorized is called when an action gets a 401: the session ended.
export function handleUnauthorized(): void {
  if (user.value !== null) showNotice('Session expired, sign in again')
  user.value = null
}

// guardSettingsTab keeps signed-out visitors off the Settings tab.
export function guardSettingsTab(mainTab: Ref<MainTab>): void {
  watch([mainTab, signedIn, loaded], () => {
    if (loaded.value && !signedIn.value && mainTab.value === 'settings') mainTab.value = 'overview'
  }, { immediate: true })
}

export function useAuth() {
  return { user, authEnabled, loaded, signedIn, notice }
}

export function resetAuthForTests(): void {
  dismissNotice()
  user.value = null
  authEnabled.value = false
  loaded.value = false
}
```

- [ ] **Step 5: Proxy `/auth` in dev.** In `frontend/vite.config.ts`, inside `server.proxy`, add next to `'/api'`:

```ts
      '/auth': {
        target: 'http://backend:4000',
        changeOrigin: true,
      },
```

- [ ] **Step 6: Widen the tab ref in `App.vue`.** `useUrlState` now takes `Ref<MainTab>` and `Ref` is invariant, so change `const mainTab = ref<'board' | 'artifacts' | 'overview'>('overview')` to `const mainTab = ref<MainTab>('overview')` and change the `useUrlState` import to `import { useUrlState, type MainTab } from './composables/useUrlState'`. Nothing else in `App.vue` changes in this task.

- [ ] **Step 7: Run tests and type check.** `cd frontend && npx vitest run src/composables/useAuth.test.ts && npx vue-tsc --noEmit` → PASS.

- [ ] **Step 8: Commit.**

```bash
git add frontend/src/composables/useAuth.ts frontend/src/composables/useAuth.test.ts frontend/src/composables/useUrlState.ts frontend/vite.config.ts frontend/src/App.vue
git commit -s -m "feat(frontend): auth state composable and settings tab type"
```

```json:metadata
{"files": ["frontend/src/composables/useAuth.ts", "frontend/src/composables/useAuth.test.ts", "frontend/src/composables/useUrlState.ts", "frontend/vite.config.ts", "frontend/src/App.vue"], "verifyCommand": "cd frontend && npx vitest run src/composables/useAuth.test.ts && npx vue-tsc --noEmit", "acceptanceCriteria": ["loadAuth sets user/authEnabled from /api/me and loaded=true even on failure", "handleUnauthorized clears user; notice only if previously signed in", "consumeAuthError maps not_allowed/failed to notices and strips auth_error/email keeping other params", "guardSettingsTab moves settings->overview once loaded and signed out", "useUrlState accepts ?tab=settings; MainTab exported", "vite proxies /auth"], "modelTier": "mechanical"}
```

---

### Task 8: Header sign-in controls, notice banner, gated Rebuild

**Goal:** Sign in / email / Settings / Sign out in `AppHeader`, a notice banner and auth bootstrapping in `App.vue`, and the Rebuild button shown only to signed-in users with 401 handling.

**Files:**
- Modify: `frontend/src/components/AppHeader.vue`
- Modify: `frontend/src/App.vue`
- Modify: `frontend/src/components/PackageCard.vue` (rebuild button at ~line 294)
- Modify: `frontend/src/composables/useRebuild.ts`
- Test: `frontend/src/components/AppHeader.test.ts`
- Test: `frontend/src/components/PackageCard.rebuild.test.ts`

**Acceptance Criteria:**
- [ ] Signed out with auth enabled: header shows a "Sign in" button and no Settings pill. Signed in: the email, a "Settings" pill (emits `update:main-tab` `'settings'`) and "Sign out". Auth disabled: none of these.
- [ ] `App.vue` calls `consumeAuthError()`, `loadAuth()` and `guardSettingsTab(mainTab)`; renders a dismissible `role="status"` banner while `notice` is set; renders `SettingsPanel` for `mainTab === 'settings'` (a placeholder component created in Step 6; Task 9 replaces it).
- [ ] Presence heartbeat is active only on `board` and `artifacts` (not `settings`).
- [ ] A failed target's "Retrigger build" button is absent when signed out and present when signed in.
- [ ] A 401 from `/api/rebuild` calls `handleUnauthorized()` and records no per-target error.

**Verify:** `cd frontend && npx vitest run src/components/AppHeader.test.ts src/components/PackageCard.rebuild.test.ts` → all PASS

**Steps:**

- [ ] **Step 1: Write the failing tests.**

`frontend/src/components/AppHeader.test.ts`:

```ts
// @vitest-environment jsdom
import { describe, it, expect, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import AppHeader from './AppHeader.vue'
import { useAuth, resetAuthForTests } from '../composables/useAuth'

function mountHeader() {
  return mount(AppHeader, { props: { theme: 'light', mainTab: 'overview' } })
}

beforeEach(() => resetAuthForTests())

describe('AppHeader auth controls', () => {
  it('offers Sign in when signed out', () => {
    useAuth().authEnabled.value = true
    const w = mountHeader()
    expect(w.find('[data-testid="sign-in"]').exists()).toBe(true)
    expect(w.find('[data-testid="settings-tab"]').exists()).toBe(false)
    expect(w.find('[data-testid="sign-out"]').exists()).toBe(false)
  })

  it('shows the user, Settings and Sign out when signed in', async () => {
    const { authEnabled, user } = useAuth()
    authEnabled.value = true
    user.value = 'alice@percona.com'
    const w = mountHeader()
    expect(w.find('[data-testid="user-email"]').text()).toBe('alice@percona.com')
    expect(w.find('[data-testid="sign-out"]').exists()).toBe(true)
    await w.find('[data-testid="settings-tab"]').trigger('click')
    expect(w.emitted('update:main-tab')).toEqual([['settings']])
  })

  it('shows no auth controls when sign-in is disabled', () => {
    const w = mountHeader()
    expect(w.find('[data-testid="sign-in"]').exists()).toBe(false)
    expect(w.find('[data-testid="sign-out"]').exists()).toBe(false)
  })
})
```

`frontend/src/components/PackageCard.rebuild.test.ts`:

```ts
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import PackageCard from './PackageCard.vue'
import { useRebuild } from '../composables/useRebuild'
import { useAuth, resetAuthForTests } from '../composables/useAuth'
import type { Package } from '../types/api'

const failing: Package = {
  project: 'ppg:staging:18',
  name: 'percona-pg_tde',
  rollup_state: 'failed',
  ok_targets: 0,
  total_targets: 1,
  targets: [{ repo: 'Debian_13', arch: 'x86_64', state: 'failed' }],
  updated_at: '2026-11-06T00:00:00Z',
} as Package

beforeEach(() => {
  resetAuthForTests()
  vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response('[]', { status: 200 }))))
})
afterEach(() => vi.unstubAllGlobals())

describe('Rebuild button', () => {
  it('is hidden when signed out', () => {
    const w = mount(PackageCard, { props: { pkg: failing } })
    expect(w.find('[aria-label="Retrigger build"]').exists()).toBe(false)
  })

  it('is shown when signed in', () => {
    useAuth().user.value = 'alice@percona.com'
    const w = mount(PackageCard, { props: { pkg: failing } })
    expect(w.find('[aria-label="Retrigger build"]').exists()).toBe(true)
  })
})

describe('useRebuild on 401', () => {
  it('signs the user out instead of recording an error', async () => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response('unauthorized', { status: 401 }))))
    const { user, notice } = useAuth()
    user.value = 'alice@percona.com'
    const { trigger, errorFor, isLoading } = useRebuild()
    await trigger('ppg:staging:18', 'percona-pg_tde', 'Debian_13', 'x86_64')
    await flushPromises()
    expect(user.value).toBeNull()
    expect(notice.value).toBe('Session expired, sign in again')
    expect(errorFor('Debian_13', 'x86_64')).toBeNull()
    expect(isLoading('Debian_13', 'x86_64')).toBe(false)
  })
})
```

- [ ] **Step 2: Run to verify failure.** `cd frontend && npx vitest run src/components/AppHeader.test.ts src/components/PackageCard.rebuild.test.ts` → FAIL (no `sign-in` element; button shown when signed out; 401 recorded as error).

- [ ] **Step 3: `useRebuild.ts`.** Add `import { handleUnauthorized } from './useAuth'` and, in `trigger`, immediately after the `const res = await fetch('/api/rebuild', …)` call:

```ts
      if (res.status === 401) {
        handleUnauthorized()
        return
      }
```

(The `finally` block still clears the loading state.)

- [ ] **Step 4: `PackageCard.vue`.** Add `import { useAuth } from '../composables/useAuth'` and `const { signedIn } = useAuth()` next to the `useRebuild()` line; change the rebuild button's `v-if="REBUILD_STATES.has(t.state)"` to `v-if="signedIn && REBUILD_STATES.has(t.state)"`.

- [ ] **Step 5: `AppHeader.vue`.** Replace the `<script setup>` block and add the controls:

```vue
<script setup lang="ts">
import type { MainTab } from '../composables/useUrlState'
import { useAuth, login, logout } from '../composables/useAuth'

defineProps<{
  theme: 'light' | 'dark'
  mainTab: MainTab
}>()
const emit = defineEmits<{
  'toggle-theme': []
  'update:main-tab': [tab: MainTab]
}>()
const { user, authEnabled, signedIn } = useAuth()

async function onSignOut() {
  await logout()
  emit('update:main-tab', 'overview')
}
</script>
```

In the template, add after the Artifacts `tab-pill` button, inside `.tab-switcher`:

```vue
      <button
        v-if="signedIn"
        data-testid="settings-tab"
        class="tab-pill"
        :class="{ active: mainTab === 'settings' }"
        @click="emit('update:main-tab', 'settings')"
      >
        Settings
      </button>
```

Wrap the theme toggle button in a right-hand group and add the auth controls before it:

```vue
    <div class="shrink-0 flex items-center gap-2">
      <template v-if="authEnabled">
        <template v-if="signedIn">
          <span data-testid="user-email" class="hidden md:inline text-[12.5px] text-text-muted font-mono truncate max-w-[220px]">{{ user }}</span>
          <button data-testid="sign-out" class="auth-btn" @click="onSignOut">Sign out</button>
        </template>
        <button v-else data-testid="sign-in" class="auth-btn" @click="login">Sign in</button>
      </template>
      <!-- existing theme toggle button, unchanged -->
    </div>
```

Add to the scoped style:

```css
.auth-btn {
  @apply inline-flex items-center px-[12px] py-2 rounded-[10px] border border-border bg-bg-card text-text-secondary text-[13px] font-semibold cursor-pointer;
  font-family: inherit;
}
```

- [ ] **Step 6: `App.vue`.**
  - Imports: `import SettingsPanel from './components/SettingsPanel.vue'` and `import { useAuth, loadAuth, consumeAuthError, guardSettingsTab, dismissNotice } from './composables/useAuth'`. (`mainTab` is already `ref<MainTab>` from Task 7.)
  - Heartbeat: `usePresenceHeartbeat(computed(() => mainTab.value === 'board' || mainTab.value === 'artifacts'))` and update its comment to "only Builds/Artifacts viewers need live OBS polling".
  - Right after the heartbeat line:

```ts
// Auth: report a failed sign-in redirect, load the session, and keep
// signed-out visitors off Settings.
const { notice } = useAuth()
consumeAuthError()
void loadAuth()
guardSettingsTab(mainTab)
```

  - Template, right after `<AppHeader … />`:

```vue
      <div
        v-if="notice"
        role="status"
        class="flex items-center justify-between gap-3 px-4 py-2 rounded-[10px] border border-border bg-bg-card text-[13px] text-text-secondary"
      >
        <span>{{ notice }}</span>
        <button class="[background:none] border-none cursor-pointer text-text-muted" aria-label="Dismiss" @click="dismissNotice">×</button>
      </div>
```

  - After the `<OverviewPanel … />` block: `<SettingsPanel v-else-if="mainTab === 'settings'" />`.
  - Create a minimal `frontend/src/components/SettingsPanel.vue` now so the app compiles (Task 9 replaces it):

```vue
<template>
  <section data-testid="access-zone"></section>
</template>
```

- [ ] **Step 7: Run tests and build.** `cd frontend && npm test && npm run build` → all PASS, build succeeds.

- [ ] **Step 8: Commit.**

```bash
git add frontend/src/components/AppHeader.vue frontend/src/components/AppHeader.test.ts frontend/src/App.vue frontend/src/components/PackageCard.vue frontend/src/components/PackageCard.rebuild.test.ts frontend/src/composables/useRebuild.ts frontend/src/components/SettingsPanel.vue
git commit -s -m "feat(frontend): sign-in controls and Rebuild only for signed-in users"
```

```json:metadata
{"files": ["frontend/src/components/AppHeader.vue", "frontend/src/components/AppHeader.test.ts", "frontend/src/App.vue", "frontend/src/components/PackageCard.vue", "frontend/src/components/PackageCard.rebuild.test.ts", "frontend/src/composables/useRebuild.ts", "frontend/src/components/SettingsPanel.vue"], "verifyCommand": "cd frontend && npm test && npm run build", "acceptanceCriteria": ["header: Sign in when signed out; email+Settings+Sign out when signed in; nothing when disabled; Settings emits 'settings'", "App calls consumeAuthError, loadAuth, guardSettingsTab; role=status dismissible notice; SettingsPanel on settings tab", "heartbeat only on board and artifacts", "Retrigger build hidden signed out, shown signed in", "401 from /api/rebuild calls handleUnauthorized and records no error"], "modelTier": "standard"}
```

---

### Task 9: Settings page Access zone

**Goal:** `SettingsPanel.vue` with the Access zone (config admins, allowed users with Remove, Add form) backed by `useSettingsUsers`.

**Files:**
- Create: `frontend/src/composables/useSettingsUsers.ts`
- Modify (replace placeholder): `frontend/src/components/SettingsPanel.vue`
- Test: `frontend/src/components/SettingsPanel.test.ts`

**Acceptance Criteria:**
- [ ] On mount, lists config admins (each with a "config" pill, no Remove) and allowed users with "added by X · YYYY-MM-DD".
- [ ] The signed-in user's own row has Remove disabled.
- [ ] Add posts `{"email": <trimmed lowercase>}`, reloads the list and clears the input on success; a 400/409 body is shown under the form.
- [ ] Remove asks `window.confirm`; on yes sends `DELETE /api/settings/users/<encoded email>` and reloads; on no sends nothing.
- [ ] A 401 from any call triggers `handleUnauthorized()`.

**Verify:** `cd frontend && npx vitest run src/components/SettingsPanel.test.ts` → all PASS

**Steps:**

- [ ] **Step 1: Write the failing tests** `frontend/src/components/SettingsPanel.test.ts`:

```ts
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import SettingsPanel from './SettingsPanel.vue'
import { useAuth, resetAuthForTests } from '../composables/useAuth'

const listBody = {
  admins: ['root@percona.com'],
  users: [
    { email: 'alice@percona.com', added_by: 'root@percona.com', added_at: '2026-11-06T12:00:00.000000000Z' },
    { email: 'bob@percona.com', added_by: 'alice@percona.com', added_at: '2026-11-07T09:30:00.000000000Z' },
  ],
}

type Handler = (url: string, init?: RequestInit) => Response
let handler: Handler
let fetchFn: ReturnType<typeof vi.fn>

beforeEach(() => {
  resetAuthForTests()
  useAuth().user.value = 'alice@percona.com'
  handler = () => new Response(JSON.stringify(listBody), { status: 200 })
  fetchFn = vi.fn((url: string, init?: RequestInit) => Promise.resolve(handler(url, init)))
  vi.stubGlobal('fetch', fetchFn)
})
afterEach(() => vi.unstubAllGlobals())

async function mounted() {
  const w = mount(SettingsPanel)
  await flushPromises()
  return w
}

describe('SettingsPanel Access zone', () => {
  it('lists admins and users', async () => {
    const w = await mounted()
    const admins = w.findAll('[data-testid="admin-row"]')
    expect(admins).toHaveLength(1)
    expect(admins[0].text()).toContain('root@percona.com')
    expect(admins[0].text()).toContain('config')
    expect(admins[0].find('[data-testid="remove"]').exists()).toBe(false)
    const users = w.findAll('[data-testid="user-row"]')
    expect(users).toHaveLength(2)
    expect(users[1].text()).toContain('added by alice@percona.com · 2026-11-07')
  })

  it('disables Remove on your own row', async () => {
    const w = await mounted()
    const [own, other] = w.findAll('[data-testid="user-row"]')
    expect(own.find('[data-testid="remove"]').attributes('disabled')).toBeDefined()
    expect(other.find('[data-testid="remove"]').attributes('disabled')).toBeUndefined()
  })

  it('adds a user and clears the input', async () => {
    const w = await mounted()
    handler = (_url, init) => init?.method === 'POST'
      ? new Response('{}', { status: 201 })
      : new Response(JSON.stringify(listBody), { status: 200 })
    await w.find('[data-testid="add-email"]').setValue('  Carol@Percona.com ')
    await w.find('form').trigger('submit')
    await flushPromises()
    const post = fetchFn.mock.calls.find(([, init]) => (init as RequestInit | undefined)?.method === 'POST')!
    expect(post[0]).toBe('/api/settings/users')
    expect(JSON.parse((post[1] as RequestInit).body as string)).toEqual({ email: 'carol@percona.com' })
    expect((w.find('[data-testid="add-email"]').element as HTMLInputElement).value).toBe('')
    expect(w.find('[data-testid="add-error"]').exists()).toBe(false)
  })

  it('shows the server error when adding fails', async () => {
    const w = await mounted()
    handler = (_url, init) => init?.method === 'POST'
      ? new Response('email must be a @percona.com address\n', { status: 400 })
      : new Response(JSON.stringify(listBody), { status: 200 })
    await w.find('[data-testid="add-email"]').setValue('eve@gmail.com')
    await w.find('form').trigger('submit')
    await flushPromises()
    expect(w.find('[data-testid="add-error"]').text()).toBe('email must be a @percona.com address')
  })

  it('removes after confirmation only', async () => {
    const w = await mounted()
    handler = (_url, init) => init?.method === 'DELETE'
      ? new Response(null, { status: 204 })
      : new Response(JSON.stringify(listBody), { status: 200 })
    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValueOnce(false).mockReturnValueOnce(true)
    const bobRemove = w.findAll('[data-testid="user-row"]')[1].find('[data-testid="remove"]')
    await bobRemove.trigger('click')
    await flushPromises()
    expect(fetchFn.mock.calls.some(([, init]) => (init as RequestInit | undefined)?.method === 'DELETE')).toBe(false)
    await bobRemove.trigger('click')
    await flushPromises()
    const del = fetchFn.mock.calls.find(([, init]) => (init as RequestInit | undefined)?.method === 'DELETE')!
    expect(del[0]).toBe('/api/settings/users/bob%40percona.com')
    confirmSpy.mockRestore()
  })

  it('signs out on 401', async () => {
    handler = () => new Response('unauthorized', { status: 401 })
    await mounted()
    expect(useAuth().user.value).toBeNull()
  })
})
```

- [ ] **Step 2: Run to verify failure.** `cd frontend && npx vitest run src/components/SettingsPanel.test.ts` → FAIL (placeholder renders nothing).

- [ ] **Step 3: Implement** `frontend/src/composables/useSettingsUsers.ts`:

```ts
import { ref } from 'vue'
import { handleUnauthorized } from './useAuth'

export interface AllowedUser {
  email: string
  added_by: string
  added_at: string
}

async function errorText(res: Response): Promise<string> {
  const text = (await res.text().catch(() => '')).trim()
  return text || `HTTP ${res.status}`
}

export function useSettingsUsers() {
  const admins = ref<string[]>([])
  const users = ref<AllowedUser[]>([])
  const loadError = ref<string | null>(null)

  async function list(): Promise<void> {
    loadError.value = null
    try {
      const res = await fetch('/api/settings/users')
      if (res.status === 401) { handleUnauthorized(); return }
      if (!res.ok) { loadError.value = await errorText(res); return }
      const body = await res.json() as { admins: string[]; users: AllowedUser[] }
      admins.value = body.admins
      users.value = body.users
    } catch {
      loadError.value = 'Network error'
    }
  }

  // add returns an error message, or null on success.
  async function add(email: string): Promise<string | null> {
    try {
      const res = await fetch('/api/settings/users', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ email: email.trim().toLowerCase() }),
      })
      if (res.status === 401) { handleUnauthorized(); return 'Session expired' }
      if (!res.ok) return await errorText(res)
      await list()
      return null
    } catch {
      return 'Network error'
    }
  }

  // remove returns an error message, or null on success.
  async function remove(email: string): Promise<string | null> {
    try {
      const res = await fetch(`/api/settings/users/${encodeURIComponent(email)}`, { method: 'DELETE' })
      if (res.status === 401) { handleUnauthorized(); return 'Session expired' }
      if (!res.ok) return await errorText(res)
      await list()
      return null
    } catch {
      return 'Network error'
    }
  }

  return { admins, users, loadError, list, add, remove }
}
```

- [ ] **Step 4: Implement** `frontend/src/components/SettingsPanel.vue` (replacing the placeholder):

```vue
<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useAuth } from '../composables/useAuth'
import { useSettingsUsers } from '../composables/useSettingsUsers'

const { user } = useAuth()
const { admins, users, loadError, list, add, remove } = useSettingsUsers()
const newEmail = ref('')
const addError = ref<string | null>(null)
const removeError = ref<string | null>(null)
const busy = ref(false)

onMounted(list)

async function onAdd() {
  if (!newEmail.value.trim()) return
  busy.value = true
  addError.value = await add(newEmail.value)
  busy.value = false
  if (addError.value === null) newEmail.value = ''
}

async function onRemove(email: string) {
  if (!window.confirm(`Remove ${email}? Their sessions end immediately.`)) return
  removeError.value = await remove(email)
}
</script>

<template>
  <div class="flex flex-col gap-5">
    <section data-testid="access-zone" class="rounded-[14px] border border-border bg-bg-card p-5 flex flex-col gap-4">
      <header class="flex flex-col gap-1">
        <h2 class="m-0 text-[16px] font-bold text-text-primary">Access</h2>
        <p class="m-0 text-[12.5px] text-text-muted">
          People who can sign in and use actions such as Rebuild. Only @percona.com Google accounts can sign in.
        </p>
      </header>

      <div v-if="loadError" data-testid="load-error" class="text-[12.5px] text-fail">{{ loadError }}</div>

      <ul class="m-0 p-0 list-none flex flex-col divide-y divide-border border border-border rounded-[10px]">
        <li v-for="a in admins" :key="'admin:' + a" data-testid="admin-row" class="flex items-center gap-3 px-3 py-2">
          <span class="font-mono text-[12.5px] text-text-primary">{{ a }}</span>
          <span class="text-[10.5px] font-bold uppercase tracking-[0.06em] px-[6px] py-[1px] rounded-[6px] bg-bg-muted text-text-muted">config</span>
        </li>
        <li v-for="u in users" :key="u.email" data-testid="user-row" class="flex items-center gap-3 px-3 py-2">
          <span class="font-mono text-[12.5px] text-text-primary">{{ u.email }}</span>
          <span class="text-[11.5px] text-text-muted">added by {{ u.added_by }} · {{ u.added_at.slice(0, 10) }}</span>
          <button
            data-testid="remove"
            class="ml-auto text-[12px] font-semibold px-[10px] py-[4px] rounded-[8px] border border-border bg-bg-card text-text-secondary cursor-pointer disabled:opacity-40 disabled:cursor-default"
            :disabled="u.email === user"
            :title="u.email === user ? 'You cannot remove yourself' : undefined"
            @click="onRemove(u.email)"
          >Remove</button>
        </li>
        <li v-if="admins.length === 0 && users.length === 0 && !loadError" class="px-3 py-2 text-[12.5px] text-text-muted">
          No one is allowed yet.
        </li>
      </ul>
      <div v-if="removeError" data-testid="remove-error" class="text-[12.5px] text-fail">{{ removeError }}</div>

      <form class="flex flex-wrap items-center gap-2" @submit.prevent="onAdd">
        <input
          v-model="newEmail"
          data-testid="add-email"
          type="email"
          aria-label="Email to allow"
          placeholder="name@percona.com"
          class="flex-1 min-w-[220px] px-3 py-2 rounded-[10px] border border-border bg-bg-app text-[13px] text-text-primary font-mono"
        />
        <button
          type="submit"
          data-testid="add"
          :disabled="busy"
          class="px-[14px] py-2 rounded-[10px] border border-border-strong bg-bg-card text-brand-purple text-[13px] font-semibold cursor-pointer disabled:opacity-50"
        >Add</button>
      </form>
      <div v-if="addError" data-testid="add-error" class="text-[12.5px] text-fail">{{ addError }}</div>
    </section>
  </div>
</template>
```

- [ ] **Step 5: Run tests and build.** `cd frontend && npm test && npm run build` → PASS.

- [ ] **Step 6: Commit.**

```bash
git add frontend/src/composables/useSettingsUsers.ts frontend/src/components/SettingsPanel.vue frontend/src/components/SettingsPanel.test.ts
git commit -s -m "feat(frontend): Settings page with allowlist management"
```

```json:metadata
{"files": ["frontend/src/composables/useSettingsUsers.ts", "frontend/src/components/SettingsPanel.vue", "frontend/src/components/SettingsPanel.test.ts"], "verifyCommand": "cd frontend && npm test && npm run build", "acceptanceCriteria": ["admins listed with config pill and no Remove; users with 'added by X · YYYY-MM-DD'", "own row Remove disabled", "Add posts trimmed lowercase email, reloads, clears input; 400/409 body shown", "Remove confirms; yes sends DELETE with encoded email; no sends nothing", "401 triggers handleUnauthorized"], "modelTier": "mechanical"}
```

---

### Task 10: Documentation and example config

**Goal:** Document Google sign-in setup and the new config keys.

**Files:**
- Modify: `README.md`
- Modify: `config.yaml.example`
- Modify: `backend/config.yaml.example`

**Acceptance Criteria:**
- [ ] README has an "Authentication" section covering: creating a Web OAuth client, both redirect URIs, `GOOGLE_CLIENT_ID`/`GOOGLE_CLIENT_SECRET`/`OBS_DASHBOARD_ADMINS`/`AUTH_PUBLIC_URL`, the allowlist on the Settings page, and that without a client ID the dashboard is read-only.
- [ ] Both example configs contain the commented `auth:` block below.

**Verify:** `grep -n "auth:" config.yaml.example backend/config.yaml.example && grep -n "## Authentication" README.md` → matches in all three files

**Steps:**

- [ ] **Step 1: Append to both `config.yaml.example` files:**

```yaml

# Google sign-in. Reads stay public; actions such as Rebuild need a
# signed-in, allowlisted @percona.com account. Sign-in is enabled only when
# GOOGLE_CLIENT_ID and GOOGLE_CLIENT_SECRET are set in the environment;
# without them the dashboard is read-only.
auth:
  # Where the dashboard is served. Used for the OAuth redirect
  # (<public_url>/auth/callback) and to check the Origin of actions.
  # Env: AUTH_PUBLIC_URL. Local dev: http://localhost:5173
  public_url: "https://dashboard.obs.pg.labs.percona.com"
  # Always allowed, cannot be removed from the Settings page. Also from the
  # comma-separated env var OBS_DASHBOARD_ADMINS.
  admins: []
  session_ttl: "168h"
```

- [ ] **Step 2: Add a README section** (after the configuration section):

```markdown
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
   the dashboard is served from. Sign-in only works from that host.

Admins and anyone they add can manage the allowlist on the **Settings** tab.
Removing someone ends their sessions immediately.

Without `GOOGLE_CLIENT_ID` the dashboard runs read-only: there is no Sign in
button and action endpoints return 401.
```

- [ ] **Step 3: Commit.**

```bash
git add README.md config.yaml.example backend/config.yaml.example
git commit -s -m "docs: Google sign-in setup and auth config"
```

```json:metadata
{"files": ["README.md", "config.yaml.example", "backend/config.yaml.example"], "verifyCommand": "grep -n 'auth:' config.yaml.example backend/config.yaml.example && grep -n '## Authentication' README.md", "acceptanceCriteria": ["README Authentication section: OAuth client, both redirect URIs, env vars, Settings allowlist, read-only without client ID", "both example configs carry the auth block"], "modelTier": "mechanical"}
```

---

### Task 11: Manual end-to-end sign-in with a real Google client

**Goal:** Prove the full flow against real Google on local dev: sign in, rebuild, manage the allowlist, sign out.

> **USER-ORDERED GATE — NON-SKIPPABLE.** This task was requested by the user in the current conversation. It MUST NOT be closed by walking around it, by declaring it "verified inline", or by substituting a cheaper check. Close only after every item in `acceptanceCriteria` has been re-validated independently, with output captured.

**Files:** none (verification only)

**Acceptance Criteria:**
- [ ] With `GOOGLE_CLIENT_ID`/`GOOGLE_CLIENT_SECRET` from the real OAuth client, `OBS_DASHBOARD_ADMINS=<your email>` and `AUTH_PUBLIC_URL=http://localhost:5173`, clicking Sign in on `http://localhost:5173` returns to the dashboard with your email in the header (backend log line `auth: signed in`).
- [ ] The Retrigger build button appears on a failed target; clicking it logs `api: rebuild triggered`.
- [ ] On Settings, adding and removing a `@percona.com` address updates the list; adding a `@gmail.com` address shows the 400 message.
- [ ] Sign out hides Retrigger build; `curl -X POST localhost:4000/api/rebuild` returns 401.
- [ ] Signing in with a non-allowlisted Percona account lands on the dashboard with "<email> is not allowed to sign in".

**Verify:** backend log contains `auth: signed in` and `api: rebuild triggered`; `curl -s -o /dev/null -w '%{http_code}' -X POST localhost:4000/api/rebuild` → `401`

**Steps:**

- [ ] **Step 1:** Obtain the OAuth client (pending confirmation from laserfocus's author about the Google Cloud project) with redirect URI `http://localhost:5173/auth/callback`.
- [ ] **Step 2:** Start the stack with the env vars above (`task dev` or `docker compose up` per README).
- [ ] **Step 3:** Walk through each acceptance criterion and capture the log lines and curl output.

```json:metadata
{"files": [], "verifyCommand": "curl -s -o /dev/null -w '%{http_code}' -X POST localhost:4000/api/rebuild", "acceptanceCriteria": ["real Google sign-in returns with email in header and 'auth: signed in' logged", "Retrigger build visible signed in; click logs 'api: rebuild triggered'", "Settings add/remove works; gmail address shows 400 message", "after sign out Retrigger build hidden and POST /api/rebuild returns 401", "non-allowlisted Percona account sees 'not allowed to sign in' notice"], "modelTier": "standard", "userGate": true, "tags": ["user-gate"], "gateScope": "local dev with real Google OAuth client"}
```
