# GitHub App Authentication for the Release Tab: Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The Release tab authenticates to GitHub as a GitHub App (installation tokens) when App credentials are configured, and Settings shows a read-only "GitHub connection" card.

**Architecture:** `release.Client` asks a `TokenSource` for a bearer token per request. `AppSource` signs an RS256 JWT, finds the installation of the configured repository, and mints and caches installation tokens. `Connection` picks the mode (`app`, `token`, `off`) from the three configured values, hands the client its token source, and runs the connection check that one new route returns to a Settings card.

**Tech Stack:** Go (chi, viper, `go-jose/v4` already in `go.mod`), Vue 3 + TypeScript, vitest.

**Spec:** `docs/superpowers/specs/2026-11-23-github-app-release-auth-design.md`. Read it before starting; this plan does not repeat its reasoning.

## Global Constraints

- No new Go or npm dependency. JWT signing uses `github.com/go-jose/go-jose/v4` and its `jwt` subpackage.
- The private key, its base64 form, an App JWT and an installation token never appear in a log line, an error string, an HTTP response or a test failure message. GitHub errors are `*release.APIError` (method, path, status) and nothing else.
- A bad, missing or half-set App configuration never fails startup.
- Env names are a contract with the deployment tooling and must not change: `RELEASE_GITHUB_APP_ID`, `RELEASE_GITHUB_APP_PRIVATE_KEY`. Config keys: `release.github_app_id`, `release.github_app_private_key`.
- Exact values: JWT `iat` = now − 60 s, `exp` = now + 9 min, alg RS256, `iss` = App ID. Token reused until 5 min before `expires_at`. A failed lookup or mint is remembered for 30 s.
- `deploy/host/obs-dashboard.container` is not changed. No host-files version bump.
- `/api/release/status` and all existing release routes keep their behaviour. The personal-token path keeps working.
- Commits: `git commit -s`, no `Co-Authored-By` trailer (CLAUDE.md). Work on branch `github-app-release-auth`.
- Backend tests: `cd backend && go test ./...`. Frontend tests: `cd frontend && npm test`; type check: `cd frontend && npx vue-tsc --noEmit`.

**User decisions (already made):**
- The App's secrets are not stored in the database; they come from the environment, deployed by the deployment tooling.
- The key is delivered base64-encoded on one env line, not as a mounted file.
- The Settings card is status and test only; nothing is editable.
- Approach A: a token source inside the release client, no `ghinstallation` library.
- The deployment tooling confirmed on 2026-11-23 that the installed App grants every permission on the required list.

## Review Focus

1. **Key value with stray whitespace.** A trailing newline or spaces around the base64 value (a hand-edited env file) must still parse. Test in Task 2 (`TestAppSourceKeyForms`).
2. **Mint response without a usable `expires_at`.** A missing or unparseable expiry must be an error, not a token cached forever. Test in Task 2 (`TestAppSourceMintBadExpiry`).
3. **Many requests at once with no token.** One Release-tab refresh issues requests concurrently; they must share one mint, not start one each. Test in Task 2 (`TestAppSourceConcurrentMint`).
4. **A non-RSA or PKCS#8 key.** A PKCS#8 RSA key must work; an EC key must give the fixed configuration error, not a panic. Test in Task 2 (`TestAppSourceKeyForms`).
5. **A permission GitHub does not list at all.** An absent permission is "none" and not ok, and must not make the whole check fail. Test in Task 3 (`TestConnectionPermissions`).

## File Structure

| File | Responsibility |
|---|---|
| `backend/internal/release/token.go` (new) | `TokenSource`, `StaticToken` |
| `backend/internal/release/app.go` (new) | `AppSource`: key parsing, JWT, installation lookup, token mint and cache |
| `backend/internal/release/connection.go` (new) | `Connection`: mode selection, token source for the client, connection check, required permissions |
| `backend/internal/release/github.go` | `Client.Token string` becomes `Client.Tokens TokenSource`; drop the token on 401 |
| `backend/internal/config/config.go` | two new release keys; `Enabled()` |
| `backend/internal/api/release_routes.go`, `server.go` | `GET /api/release/github` |
| `backend/cmd/obsboard/main.go` | build the `Connection`, wire client and route |
| `frontend/src/types/release.ts` | `GitHubConnection` type |
| `frontend/src/composables/useGitHubConnection.ts` (new) | load the status |
| `frontend/src/components/GitHubConnectionCard.vue` (new) | the card |
| `frontend/src/components/SettingsPanel.vue` | place the card |

---

### Task 1: Token source in the release client

**Goal:** `release.Client` gets its bearer token from a `TokenSource` per request instead of a fixed string.

**Files:**
- Create: `backend/internal/release/token.go`
- Modify: `backend/internal/release/github.go` (struct at 113-123, `do` at 164-241)
- Modify: `backend/internal/release/github_test.go:23`, `backend/cmd/obsboard/main.go:219`
- Test: `backend/internal/release/github_test.go`

**Acceptance Criteria:**
- [ ] `Client` has `Tokens TokenSource` and no `Token` field.
- [ ] A token-source error is returned from the request unchanged and no HTTP request is sent.
- [ ] A 401 response calls `Invalidate(token)` on a source that implements it; the request is not retried.
- [ ] All existing tests in `internal/release` pass.

**Verify:** `cd backend && go test ./internal/release/ ./cmd/...` → `ok`

**Steps:**

- [ ] **Step 1: Write the failing tests** in `github_test.go`

```go
type countingSource struct {
	tok         string
	err         error
	invalidated []string
}

func (s *countingSource) Token(context.Context) (string, error) { return s.tok, s.err }
func (s *countingSource) Invalidate(tok string)                 { s.invalidated = append(s.invalidated, tok) }

func TestClientTokenSourceError(t *testing.T) {
	hits := 0
	c := newGHClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	want := errors.New("no token")
	c.Tokens = &countingSource{err: want}
	if _, err := c.OpenPulls(context.Background()); !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
	if hits != 0 {
		t.Fatalf("sent %d requests, want 0", hits)
	}
}

func TestClientInvalidatesOn401(t *testing.T) {
	hits := 0
	c := newGHClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusUnauthorized)
	}))
	src := &countingSource{tok: "ghs_OLD"}
	c.Tokens = src
	_, err := c.OpenPulls(context.Background())
	var ae *APIError
	if !errors.As(err, &ae) || ae.Status != 401 {
		t.Fatalf("err = %v, want APIError 401", err)
	}
	if hits != 1 || len(src.invalidated) != 1 || src.invalidated[0] != "ghs_OLD" {
		t.Fatalf("hits=%d invalidated=%v", hits, src.invalidated)
	}
}
```

Change `newGHClient` to `Tokens: StaticToken("ghp_SECRET")`.

- [ ] **Step 2: Run** `cd backend && go test ./internal/release/ -run 'TestClient(TokenSourceError|InvalidatesOn401)'` → FAIL (does not compile: `Tokens` undefined).

- [ ] **Step 3: Create `token.go`**

```go
// TokenSource supplies the bearer token for one GitHub request.
type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

// StaticToken is a TokenSource for a fixed token (a personal access token).
type StaticToken string

func (s StaticToken) Token(context.Context) (string, error) { return string(s), nil }
```

- [ ] **Step 4: Change `Client`.** Replace the `Token string` field with `Tokens TokenSource`. In `do`, obtain the token before building the request and return its error as is. After the response, when the status is 401 and `c.Tokens` implements `interface{ Invalidate(string) }`, call it with the token used. In `main.go:219` pass `Tokens: release.StaticToken(cfg.Release.GitHubToken)` (Task 4 replaces this line).

- [ ] **Step 5: Run** `cd backend && go test ./internal/release/ ./cmd/...` → `ok`.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/release/token.go backend/internal/release/github.go backend/internal/release/github_test.go backend/cmd/obsboard/main.go
git commit -s -m "refactor(release): take the GitHub token from a token source"
```

```json:metadata
{"files": ["backend/internal/release/token.go", "backend/internal/release/github.go", "backend/internal/release/github_test.go", "backend/cmd/obsboard/main.go"], "verifyCommand": "cd backend && go test ./internal/release/ ./cmd/...", "acceptanceCriteria": ["Client has Tokens TokenSource and no Token field", "token-source error returned unchanged with no HTTP request", "401 calls Invalidate(token) once, no retry", "existing release tests pass"], "modelTier": "mechanical"}
```

---

### Task 2: AppSource: installation tokens for a GitHub App

**Goal:** A `TokenSource` that turns an App ID and a base64 PEM key into cached installation tokens for one repository.

**Files:**
- Create: `backend/internal/release/app.go`
- Test: `backend/internal/release/app_test.go`

**Interfaces (used by Task 3 and 4):**

```go
// NewAppSource never fails: a configuration fault is kept and returned by
// every call. appID and keyB64 are trimmed of surrounding whitespace.
func NewAppSource(repo, appID, keyB64 string) *AppSource

type AppSource struct {
	APIBase string           // default https://api.github.com
	HTTP    *http.Client     // default: 30 s timeout
	Now     func() time.Time // default time.Now
	// unexported: repo, appID, key, cfgErr, mu, installID, token, mintedAt,
	// expiresAt, lastErr, lastErrAt, failUntil
}

func (a *AppSource) Token(ctx context.Context) (string, error) // TokenSource
func (a *AppSource) Invalidate(token string)                   // drops the cached token if it is this one
func (a *AppSource) ConfigErr() error                          // nil when ID and key are usable
func (a *AppSource) App(ctx context.Context) (AppInfo, error)  // GET /app with the JWT
func (a *AppSource) Installation(ctx context.Context) (Installation, error) // live lookup, ignores the failure memory, stores the ID
func (a *AppSource) FreshToken(ctx context.Context) (string, error) // like Token but ignores the 30 s failure memory
func (a *AppSource) TokenState() TokenState

type AppInfo struct{ ID int64; Slug, Name string }
type Installation struct {
	ID          int64
	Permissions map[string]string // GitHub's "permissions" object
}
type TokenState struct {
	MintedAt, ExpiresAt time.Time // zero when no token is cached
	LastError           string    // sanitised; "" when the last mint succeeded
	LastErrorAt         time.Time
}
```

Configuration errors are these exact strings (wrap a package-level `ErrAppConfig` so callers can use `errors.Is`):

- `RELEASE_GITHUB_APP_ID is not set`
- `RELEASE_GITHUB_APP_PRIVATE_KEY is not set`
- `RELEASE_GITHUB_APP_PRIVATE_KEY is not valid base64`
- `RELEASE_GITHUB_APP_PRIVATE_KEY is not an RSA private key in PEM form`

GitHub calls:

| Call | Auth | Notes |
|---|---|---|
| `GET /app` | JWT | fields `id`, `slug`, `name` |
| `GET /repos/{repo}/installation` | JWT | fields `id`, `permissions`; 404 → `*APIError` wrapping `ErrNotFound`, ID not stored |
| `POST /app/installations/{id}/access_tokens` | JWT | body `{"repositories":["<name part of repo>"]}`; response `token`, `expires_at` (RFC 3339) |

All three send `Accept: application/vnd.github+json` and `X-GitHub-Api-Version: 2022-11-28`. Non-2xx → `*APIError{Method, Path, Status}`; transport failure → `fmt.Errorf("github %s %s: request failed", method, path)`, as in `Client.do`.

**Acceptance Criteria:**
- [ ] The JWT verifies against the key's public half with RS256 and has `iss` = App ID, `iat` = now − 60 s, `exp` = now + 9 min.
- [ ] The first `Token` does one lookup and one mint; later calls reuse the token until 5 min before expiry, then mint again without a second lookup.
- [ ] `Invalidate` with the cached token forces a mint on the next call; with another string it does nothing.
- [ ] After a failed lookup or mint, `Token` returns the same error for 30 s without calling GitHub; `FreshToken` and `Installation` call GitHub anyway.
- [ ] Every configuration fault returns its fixed string from `Token`, `App`, `Installation` and `FreshToken`, and no HTTP request is made.
- [ ] No error string or `TokenState` field contains the key, the JWT or a token.

**Verify:** `cd backend && go test ./internal/release/ -run TestAppSource -race` → `ok`

**Steps:**

- [ ] **Step 1: Write the test helper and failing tests** in `app_test.go`. The helper is a fake App API reused by Task 3:

```go
// fakeAppAPI serves the three App endpoints. Zero value: App installed, all
// calls succeed, tokens "ghs_1", "ghs_2", … expiring one hour after now().
type fakeAppAPI struct {
	mu          sync.Mutex
	key         *rsa.PrivateKey // JWTs are verified against this
	now         func() time.Time
	appStatus   int               // non-zero: GET /app answers this status
	instStatus  int               // non-zero: installation lookup answers this status
	mintStatus  int               // non-zero: mint answers this status
	expiresAt   string            // non-empty: literal expires_at in the mint response
	permissions map[string]string // nil: the full required set
	mintDelay   time.Duration
	lookups, mints int
	lastClaims  jwt.Claims
	lastMintBody string
}

func (f *fakeAppAPI) ServeHTTP(w http.ResponseWriter, r *http.Request)

// newTestApp returns a source wired to a fake API, with a fixed clock the
// test can move through *clock.
func newTestApp(t *testing.T) (src *AppSource, api *fakeAppAPI, clock *time.Time)

func testKeyB64(t *testing.T, key *rsa.PrivateKey, pkcs8 bool) string
```

`ServeHTTP` rejects a request whose bearer JWT does not verify against `key` with status 401, records the claims, and answers installation ID `7654321`, app `{id:123456, slug:"percona-obs-release", name:"Percona OBS Release"}`.

Tests, with their assertions:

```go
func TestAppSourceJWTClaims(t *testing.T)
// after Token(): api.lastClaims.Issuer == "123456";
// IssuedAt == clock-60s; Expiry == clock+9m (compare Unix seconds).

func TestAppSourceMintReuseRefresh(t *testing.T)
// Token() == "ghs_1", lookups==1, mints==1
// clock += 54m; Token() == "ghs_1", mints==1
// clock += 2m (56m, inside the 5 min margin); Token() == "ghs_2", lookups==1, mints==2
// api.lastMintBody == `{"repositories":["obs-packaging"]}`

func TestAppSourceInvalidate(t *testing.T)
// Token()=="ghs_1"; Invalidate("other"); Token()=="ghs_1";
// Invalidate("ghs_1"); Token()=="ghs_2"

func TestAppSourceNotInstalled(t *testing.T)
// api.instStatus=404: Token() err is *APIError{GET, /repos/percona/obs-packaging/installation, 404}
// and errors.Is(err, ErrNotFound); api.instStatus=0, clock += 31s: Token()=="ghs_1"

func TestAppSourceFailureMemory(t *testing.T)
// api.mintStatus=500: Token() fails, mints==1; Token() again fails with the
// same error text, mints==1; FreshToken() fails, mints==2;
// clock += 31s; api.mintStatus=0; Token()=="ghs_1"
// TokenState().LastError == "github POST /app/installations/7654321/access_tokens: HTTP 500"
// after the success, TokenState().LastError == ""

func TestAppSourceMintBadExpiry(t *testing.T)
// table: api.expiresAt = "not-a-time", and a response with no expires_at:
// Token() returns an error, TokenState().ExpiresAt.IsZero()

func TestAppSourceConcurrentMint(t *testing.T)
// api.mintDelay = 50ms; 20 goroutines call Token(); all get "ghs_1";
// lookups==1, mints==1. Run with -race.

func TestAppSourceKeyForms(t *testing.T)
// table over NewAppSource("percona/obs-packaging", id, key):
//   PKCS#1 key                      -> ConfigErr()==nil
//   PKCS#8 RSA key                  -> ConfigErr()==nil
//   "  "+PKCS#1+"\n"                -> ConfigErr()==nil
//   id=""                           -> "RELEASE_GITHUB_APP_ID is not set"
//   key=""                          -> "RELEASE_GITHUB_APP_PRIVATE_KEY is not set"
//   key="!!!"                       -> "RELEASE_GITHUB_APP_PRIVATE_KEY is not valid base64"
//   base64("hello")                 -> "...is not an RSA private key in PEM form"
//   base64(PKCS#8 EC P-256 key PEM) -> "...is not an RSA private key in PEM form"
// every error case: errors.Is(err, ErrAppConfig); Token(), App(),
// Installation(), FreshToken() return it; the fake API saw 0 requests.

func TestAppSourceNoSecretInErrors(t *testing.T)
// for appStatus=401, instStatus=500, mintStatus=403 and a closed server:
// the error text contains neither the base64 key, "BEGIN", "eyJ" nor "ghs_".
```

- [ ] **Step 2: Run** `cd backend && go test ./internal/release/ -run TestAppSource` → FAIL (does not compile).

- [ ] **Step 3: Implement `app.go`** to the interfaces above. Sign with `jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, (&jose.SignerOptions{}).WithType("JWT"))` and `jwt.Signed(signer).Claims(jwt.Claims{Issuer, IssuedAt, Expiry}).Serialize()`. Parse the key with `pem.Decode` then `x509.ParsePKCS1PrivateKey`, falling back to `x509.ParsePKCS8PrivateKey` with a type assertion to `*rsa.PrivateKey`. Hold one mutex across lookup and mint so concurrent callers share one mint. `Token` returns the remembered error while `Now()` is before `failUntil`; `FreshToken` skips that test. Both record success or failure in the same state.

- [ ] **Step 4: Run** `cd backend && go test ./internal/release/ -run TestAppSource -race` → `ok`.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/release/app.go backend/internal/release/app_test.go
git commit -s -m "feat(release): mint GitHub App installation tokens"
```

```json:metadata
{"files": ["backend/internal/release/app.go", "backend/internal/release/app_test.go"], "verifyCommand": "cd backend && go test ./internal/release/ -run TestAppSource -race", "acceptanceCriteria": ["JWT RS256 with iss=App ID, iat=now-60s, exp=now+9m", "one lookup and one mint, reuse until 5 min before expiry", "Invalidate drops only the matching token", "30 s failure memory bypassed by FreshToken and Installation", "fixed configuration error strings, no HTTP request", "no secret in errors or TokenState"], "modelTier": "standard"}
```

---

### Task 3: Connection: mode selection and the connection check

**Goal:** One type decides the mode from the configured values, supplies the client's token source, and produces the status the Settings card shows.

**Files:**
- Create: `backend/internal/release/connection.go`
- Test: `backend/internal/release/connection_test.go`

**Interfaces (used by Task 4):**

```go
const (
	ModeApp   = "app"
	ModeToken = "token"
	ModeOff   = "off"
)

// NewConnection picks the mode: app when appID or keyB64 is non-empty (even
// if only one is, or the key is unusable), else token when pat is non-empty,
// else off. Inputs are trimmed.
func NewConnection(repo, appID, keyB64, pat string) *Connection

type Connection struct {
	APIBase string           // passed on to the AppSource and used for the token-mode call
	HTTP    *http.Client
	Now     func() time.Time
	// unexported: mode, repo, app *AppSource, pat
}

func (c *Connection) Mode() string
func (c *Connection) ConfigErr() error          // the AppSource's, in app mode; else nil
func (c *Connection) TokenSource() TokenSource  // *AppSource, StaticToken, or nil when off
func (c *Connection) Check(ctx context.Context) ConnStatus

type ConnStatus struct {
	Mode           string           `json:"mode"`
	Repo           string           `json:"repo"`
	OK             bool             `json:"ok"`
	Error          string           `json:"error"`
	FailedStep     string           `json:"failed_step"`
	App            *ConnApp         `json:"app,omitempty"`
	InstallationID int64            `json:"installation_id,omitempty"`
	Permissions    []ConnPermission `json:"permissions,omitempty"`
	Token          *ConnToken       `json:"token,omitempty"`
	CheckedAt      time.Time        `json:"checked_at"`
}
type ConnApp struct {
	ID   string `json:"id"` // GitHub's numeric ID, as a string
	Slug string `json:"slug"`
	Name string `json:"name"`
}
type ConnPermission struct {
	Name     string `json:"name"`
	Required string `json:"required"`
	Granted  string `json:"granted"` // "none" when GitHub does not list it
	OK       bool   `json:"ok"`
}
type ConnToken struct {
	MintedAt    *time.Time `json:"minted_at"`
	ExpiresAt   *time.Time `json:"expires_at"`
	LastError   string     `json:"last_error"`
	LastErrorAt *time.Time `json:"last_error_at"`
}
```

`APIBase`, `HTTP` and `Now` set on the `Connection` must reach its `AppSource` (copy them on use, so tests can set them after `NewConnection`).

Required permissions, one package-level ordered list, in this order: `contents` write, `pull_requests` write, `actions` write, `checks` read, `metadata` read. Levels order: none < read < write < admin.

`Check` in mode `app` runs these steps and stops at the first failure, setting `FailedStep` and `Error` (the error's text) and `OK=false`, keeping what earlier steps filled in:

| Step | `FailedStep` | Fills |
|---|---|---|
| `ConfigErr()` non-nil | `config` | nothing |
| `App(ctx)` | `app` | `App` |
| `Installation(ctx)` | `installation` | `InstallationID`, `Permissions` |
| `FreshToken(ctx)` | `token` | nothing |

`Token` (from `TokenState()`, zero times as `null`) is set whenever the config step passed. Missing permissions do not change `OK`. Mode `token`: `GET /repos/{repo}` with the token; failure → `FailedStep="repository"`. Mode `off`: `OK=false`, no call, no error. `CheckedAt` is `Now()` in UTC in every mode.

**Acceptance Criteria:**
- [ ] `NewConnection` yields the mode of every row of the spec § 5 table.
- [ ] A healthy app-mode check has `OK=true`, the app, installation `7654321`, five permission rows all ok, and a token with both times set.
- [ ] Each `FailedStep` value is produced by the fault that the table names, with the sanitised error.
- [ ] A permission GitHub omits is `granted:"none", ok:false`; a `read` grant for a `write` requirement is not ok; `admin` satisfies `write`; `OK` stays true.
- [ ] `json.Marshal(Check(...))` contains neither the key, a JWT nor a token in any mode.

**Verify:** `cd backend && go test ./internal/release/ -run TestConnection` → `ok`

**Steps:**

- [ ] **Step 1: Write the failing tests** in `connection_test.go`, using `fakeAppAPI` and `testKeyB64` from Task 2 and a helper `newTestConn(t, pat string) (*Connection, *fakeAppAPI)` that also serves `GET /repos/percona/obs-packaging` (status settable through a `repoStatus int` field added to `fakeAppAPI`).

```go
func TestConnectionMode(t *testing.T)
// table (id, key, pat) -> Mode(), ConfigErr()!=nil, TokenSource()==nil:
//   ("1", good, "")      app,   false, false
//   ("1", good, "ghp_x") app,   false, false
//   ("1", "",   "ghp_x") app,   true,  false
//   ("",  good, "")      app,   true,  false
//   ("1", "!!!", "ghp_x") app,  true,  false
//   ("",  "",   "ghp_x") token, false, false   (TokenSource() == StaticToken("ghp_x"))
//   ("",  "",   "")      off,   false, true
//   (" ", " ",  " ")     off,   false, true

func TestConnectionCheckApp(t *testing.T)
// healthy: Mode "app", Repo "percona/obs-packaging", OK, Error=="", FailedStep=="",
// App == {ID:"123456", Slug:"percona-obs-release", Name:"Percona OBS Release"},
// InstallationID == 7654321, len(Permissions)==5 in the list order, all OK,
// Token.MintedAt and Token.ExpiresAt non-nil, CheckedAt == clock in UTC.

func TestConnectionCheckFailedSteps(t *testing.T)
// table:
//   key "!!!"            -> "config", Error "RELEASE_GITHUB_APP_PRIVATE_KEY is not valid base64", App==nil, Token==nil
//   api.appStatus=401    -> "app", Error "github GET /app: HTTP 401", App==nil, Token!=nil
//   api.instStatus=404   -> "installation", Error "github GET /repos/percona/obs-packaging/installation: HTTP 404", App!=nil, Permissions==nil
//   api.mintStatus=403   -> "token", Error "github POST /app/installations/7654321/access_tokens: HTTP 403", Permissions!=nil, Token.LastError==Error
// OK is false in each.

func TestConnectionCheckIgnoresFailureMemory(t *testing.T)
// api.mintStatus=500; TokenSource().Token() fails; api.mintStatus=0;
// Check().OK == true without moving the clock.

func TestConnectionPermissions(t *testing.T)
// api.permissions = {"contents":"read","pull_requests":"admin","actions":"write","metadata":"read"}
// -> rows: contents{required write, granted read, ok false},
//    pull_requests{granted admin, ok true}, actions ok,
//    checks{granted "none", ok false}, metadata ok; status OK == true.

func TestConnectionCheckToken(t *testing.T)
// pat mode: OK true, App==nil, Token==nil, the request carried "Bearer ghp_x";
// repoStatus=404 -> FailedStep "repository",
// Error "github GET /repos/percona/obs-packaging: HTTP 404".

func TestConnectionCheckOff(t *testing.T)
// Mode "off", OK false, Error "", FailedStep "", zero requests.

func TestConnectionCheckNoSecrets(t *testing.T)
// for the healthy app case, each failed step, and token mode: the marshalled
// JSON contains none of: the base64 key, "BEGIN", "eyJ", "ghs_", "ghp_x".
```

- [ ] **Step 2: Run** `cd backend && go test ./internal/release/ -run TestConnection` → FAIL (does not compile).

- [ ] **Step 3: Implement `connection.go`** to the interfaces and the step table above.

- [ ] **Step 4: Run** `cd backend && go test ./internal/release/ -race` → `ok`.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/release/connection.go backend/internal/release/connection_test.go backend/internal/release/app_test.go
git commit -s -m "feat(release): choose the GitHub auth mode and check the connection"
```

```json:metadata
{"files": ["backend/internal/release/connection.go", "backend/internal/release/connection_test.go"], "verifyCommand": "cd backend && go test ./internal/release/ -run TestConnection", "acceptanceCriteria": ["mode for every row of the spec table", "healthy app check fully populated", "each failed_step produced with its sanitised error", "absent permission is none/not ok and does not fail the check", "no secret in the marshalled status"], "modelTier": "standard"}
```

---

### Task 4: Config, wiring and the status route

**Goal:** The dashboard reads the two new values, runs the Release tab on the chosen token source, and serves `GET /api/release/github`.

**Files:**
- Modify: `backend/internal/config/config.go` (bind list near line 366, `ReleaseConfig` and `loadRelease` at 523-542)
- Modify: `backend/internal/api/release_routes.go`, `backend/internal/api/server.go:48` and the release group at 124-131
- Modify: `backend/cmd/obsboard/main.go:214-225`
- Modify: every `NewRouter(` call in `backend/internal/api/*_test.go` (12 call sites) for the new parameter
- Test: `backend/internal/config/config_test.go`, `backend/internal/api/release_routes_test.go`

**Interfaces:**

```go
// config
type ReleaseConfig struct {
	GitHubToken         string // RELEASE_GITHUB_TOKEN: personal access token, the fallback
	GitHubRepo          string
	GitHubAppID         string // RELEASE_GITHUB_APP_ID
	GitHubAppPrivateKey string // RELEASE_GITHUB_APP_PRIVATE_KEY: base64 of the PEM
}
// Enabled: any of GitHubToken, GitHubAppID, GitHubAppPrivateKey is non-empty.

// api
type GitHubChecker interface {
	Check(ctx context.Context) release.ConnStatus
}
func NewRouter(db *sql.DB, h *hub.Hub, fleet *obs.Fleet, ws Statter, telemetryEnabled *atomic.Bool, telemetryInterval time.Duration, gate PresenceGate, authSvc *auth.Service, upg *upgrade.Manager, rescan CveRescanner, rel ReleaseService, ghc GitHubChecker, logs LogSource) http.Handler
```

`ghc` goes directly after `rel`. The route is registered in the existing signed-in release group, outside the `if rel != nil` block, when `ghc != nil`. Existing tests pass `nil`.

`main.go`: build `conn := release.NewConnection(cfg.Release.GitHubRepo, cfg.Release.GitHubAppID, cfg.Release.GitHubAppPrivateKey, cfg.Release.GitHubToken)` always; when `conn.Mode() != release.ModeOff` create the service with `&release.Client{Repo: …, Tokens: conn.TokenSource()}`; pass `conn` as `ghc` in every mode. Log `slog.Info("release: enabled", "repo", repo, "mode", conn.Mode())`, and `slog.Warn("release: GitHub App misconfigured", "error", conn.ConfigErr())` when it is non-nil. No other field is logged.

**Acceptance Criteria:**
- [ ] `RELEASE_GITHUB_APP_ID` and `RELEASE_GITHUB_APP_PRIVATE_KEY` load into the config, trimmed; `config.Load` succeeds with a half-set pair and with a garbage key.
- [ ] `Enabled()` is true when only the App ID is set, only the key is set, or only the token is set; false when none is.
- [ ] `GET /api/release/github` answers 401 without a session, and 200 with the checker's status as JSON for a signed-in user, both when the release service is nil and when it is set.
- [ ] `/api/release/status` behaves as before.
- [ ] `cd backend && go vet ./... && go test ./...` passes.

**Verify:** `cd backend && go vet ./... && go test ./...` → `ok` for every package

**Steps:**

- [ ] **Step 1: Write the failing config test** in `config_test.go`, next to `TestReleaseConfigDefaultsAndEnv`, using that test's own setup helper:

```go
func TestReleaseConfigApp(t *testing.T) {
	// no release env: !cfg.Release.Enabled()
	// RELEASE_GITHUB_APP_ID=" 123456 ", RELEASE_GITHUB_APP_PRIVATE_KEY=" abc= ":
	//   Load err == nil; GitHubAppID == "123456"; GitHubAppPrivateKey == "abc="; Enabled()
	// only RELEASE_GITHUB_APP_ID=1: err == nil, Enabled()
	// only RELEASE_GITHUB_APP_PRIVATE_KEY="!!!": err == nil, Enabled()
}
```

- [ ] **Step 2: Write the failing route test** in `release_routes_test.go`. Extend the file's router helper (line 98) to take a `GitHubChecker`, and add:

```go
type fakeChecker struct{ st release.ConnStatus }

func (f fakeChecker) Check(context.Context) release.ConnStatus { return f.st }

func TestReleaseGitHubRoute(t *testing.T) {
	// checker status: {Mode:"app", Repo:"percona/obs-packaging", OK:true,
	//   App:&release.ConnApp{ID:"123456", Slug:"s", Name:"n"}, InstallationID:7}
	// with rel == nil:
	//   no session  -> 401
	//   signed in   -> 200, Content-Type application/json, body decodes to
	//                  mode "app", ok true, app.id "123456", installation_id 7
	// with rel set  -> same 200 body
	// status {Mode:"off"} -> 200 and the body has no "app", "permissions" or "token" key
}
```

- [ ] **Step 3: Run** `cd backend && go test ./internal/config/ ./internal/api/` → FAIL (does not compile).

- [ ] **Step 4: Implement.** Add the two `BindEnv` pairs (`release.github_app_id`/`RELEASE_GITHUB_APP_ID`, `release.github_app_private_key`/`RELEASE_GITHUB_APP_PRIVATE_KEY`), the fields, and the new `Enabled()`. Add `GitHubChecker`, the handler (`relJSON(w, http.StatusOK, ghc.Check(r.Context()))`), the `NewRouter` parameter and the route. Update the 12 test call sites with `nil`. Wire `main.go` as described.

- [ ] **Step 5: Run** `cd backend && go vet ./... && go test ./...` → `ok`.

- [ ] **Step 6: Commit**

```bash
git add backend
git commit -s -m "feat(release): authenticate as a GitHub App and expose the connection status"
```

```json:metadata
{"files": ["backend/internal/config/config.go", "backend/internal/config/config_test.go", "backend/internal/api/release_routes.go", "backend/internal/api/server.go", "backend/internal/api/release_routes_test.go", "backend/cmd/obsboard/main.go"], "verifyCommand": "cd backend && go vet ./... && go test ./...", "acceptanceCriteria": ["two env values load trimmed; Load succeeds with half-set or garbage values", "Enabled() true when any of the three is set", "GET /api/release/github: 401 unsigned, 200 with checker JSON when rel is nil or set", "/api/release/status unchanged", "go vet and go test pass"], "modelTier": "standard"}
```

---

### Task 5: GitHub connection card in Settings

**Goal:** Settings → General shows a read-only "GitHub connection" card with a "Test connection" button.

**Files:**
- Modify: `frontend/src/types/release.ts`
- Create: `frontend/src/composables/useGitHubConnection.ts`
- Create: `frontend/src/components/GitHubConnectionCard.vue`
- Modify: `frontend/src/components/SettingsPanel.vue` (between `<ServiceZone>` and the access section)
- Modify: `frontend/src/components/SettingsPanel.test.ts` (default fetch handler)
- Test: `frontend/src/components/GitHubConnectionCard.test.ts`

**Interfaces:**

```ts
// types/release.ts — mirrors release.ConnStatus
export interface GitHubConnection {
  mode: 'app' | 'token' | 'off'
  repo: string
  ok: boolean
  error: string
  failed_step: '' | 'config' | 'app' | 'installation' | 'token' | 'repository'
  app?: { id: string; slug: string; name: string }
  installation_id?: number
  permissions?: { name: string; required: string; granted: string; ok: boolean }[]
  token?: { minted_at: string | null; expires_at: string | null; last_error: string; last_error_at: string | null }
  checked_at: string
}

// composables/useGitHubConnection.ts — same shape as useSettingsUsers
export function useGitHubConnection(): {
  status: Ref<GitHubConnection | null>
  loadError: Ref<string | null>
  loading: Ref<boolean>
  load: () => Promise<void> // GET /api/release/github; 401 -> handleUnauthorized()
}
```

The card loads on mount. Layout and classes follow the Access section in `SettingsPanel.vue` (`rounded-[14px] border border-border bg-bg-card p-5 flex flex-col gap-4`, same header sizes). Root `data-testid="github-zone"`. The spec § 7 has the text sketches.

Exact copy:

| Where | Text |
|---|---|
| Heading | `GitHub connection` |
| Sub-heading, mode app | `The Release tab acts on GitHub as this app.` |
| Button (`data-testid="github-test"`) | `Test connection`; disabled while `loading` |
| State (`data-testid="github-state"`), `ok` | `Connected` |
| State, not ok, mode app or token | `Not connected` |
| `failed_step` `config` (`data-testid="github-reason"`) | the `error` text itself, and no separate error line |
| `app` | `GitHub rejected the app's key or ID.` |
| `installation`, error ends `HTTP 404` | `The app is not installed on <repo>.` |
| `installation`, otherwise | `Could not look up the app's installation.` |
| `token` | `GitHub would not issue a token.` |
| `repository` | `The token cannot reach <repo>.` |
| Error line (`data-testid="github-error"`) | the `error` text, monospace |
| App row | `<name> (<slug>) · app <id>` |
| Repository row | `<repo> · installation <installation_id>` (mode token: `<repo>`) |
| Permission (`data-testid="github-perm"`, one per row) | `<name> <granted>`; ok rows end ` ✓`; not-ok rows use `text-warn` if that class exists in `theme.css`, else `text-fail` |
| Any permission not ok (`data-testid="github-perm-warning"`) | `Some release actions will fail until the app is granted these permissions.` |
| Token row | `issued <HH:MM>, expires <HH:MM>` in UTC, from the ISO strings (`slice(11, 16)`); omitted when `minted_at` is null |
| Checked row | `<HH:MM:SS> UTC` from `checked_at` |
| Mode token (`data-testid="github-token-mode"`) | `Using a personal access token. Set RELEASE_GITHUB_APP_ID and RELEASE_GITHUB_APP_PRIVATE_KEY to switch to the GitHub App.` |
| Mode off (`data-testid="github-off"`) | `Not configured. The Release tab is hidden.` followed by the two variable names in monospace |
| Request failed (`data-testid="github-load-error"`) | the `loadError` text; the button stays usable |

**Acceptance Criteria:**
- [ ] The four states (app connected, app not connected, token mode, off) render the copy above.
- [ ] "Test connection" issues a second `GET /api/release/github` and is disabled while it is in flight.
- [ ] A failed request shows the load error and leaves the button enabled.
- [ ] The card appears in Settings → General between the Service card and the Access card, and not in the Server log sub-tab.
- [ ] `cd frontend && npm test && npx vue-tsc --noEmit` passes.

**Verify:** `cd frontend && npm test && npx vue-tsc --noEmit` → all tests pass, no type errors

**Steps:**

- [ ] **Step 1: Write the failing tests** in `GitHubConnectionCard.test.ts`, with the fetch stub pattern of `SettingsPanel.test.ts` (`vi.stubGlobal('fetch', …)`, `resetAuthForTests()`, `mount` + `flushPromises`).

```ts
const healthy: GitHubConnection = {
  mode: 'app', repo: 'percona/obs-packaging', ok: true, error: '', failed_step: '',
  app: { id: '123456', slug: 'percona-obs-release', name: 'Percona OBS Release' },
  installation_id: 7654321,
  permissions: [
    { name: 'contents', required: 'write', granted: 'write', ok: true },
    { name: 'checks', required: 'read', granted: 'read', ok: true },
  ],
  token: { minted_at: '2026-11-23T14:02:00Z', expires_at: '2026-11-23T15:02:00Z', last_error: '', last_error_at: null },
  checked_at: '2026-11-23T14:31:07Z',
}

it('shows a connected app')
// state "Connected"; text contains "Percona OBS Release (percona-obs-release) · app 123456",
// "percona/obs-packaging · installation 7654321", "issued 14:02, expires 15:02", "14:31:07 UTC";
// 2 perm rows, first "contents write ✓"; no github-perm-warning, github-error, github-off.

it('explains an app that is not installed')
// {...healthy, ok:false, failed_step:'installation', installation_id: undefined, permissions: undefined,
//  error:'github GET /repos/percona/obs-packaging/installation: HTTP 404'}
// state "Not connected"; reason "The app is not installed on percona/obs-packaging.";
// github-error has the error text.

it.each([
  ['app', 'github GET /app: HTTP 401', "GitHub rejected the app's key or ID."],
  ['installation', 'github GET /repos/percona/obs-packaging/installation: HTTP 500', "Could not look up the app's installation."],
  ['token', 'github POST /app/installations/7654321/access_tokens: HTTP 403', 'GitHub would not issue a token.'],
])('names the failed step %s')

it('shows a configuration error once')
// failed_step 'config', error 'RELEASE_GITHUB_APP_PRIVATE_KEY is not valid base64':
// reason equals the error; github-error does not exist.

it('warns about a missing permission')
// permissions [{name:'checks', required:'read', granted:'none', ok:false}, contents ok]:
// a perm row with text "checks none" and no "✓"; github-perm-warning exists; state still "Connected".

it('shows token mode')
// {mode:'token', repo:'percona/obs-packaging', ok:true, error:'', failed_step:'', checked_at:…}
// github-token-mode exists with the copy above; text contains the repo; no perm rows.

it('shows off mode')
// {mode:'off', ok:false, …}: github-off contains "Not configured. The Release tab is hidden."
// and both variable names; github-state does not exist.

it('re-tests on click')
// fetch resolves on demand: after mount 1 call; click github-test -> 2 calls to
// '/api/release/github', the button has the disabled attribute until the second resolves.

it('shows a load error and keeps the button')
// fetch -> 500 "boom": github-load-error text "boom"; button not disabled.
```

- [ ] **Step 2: Run** `cd frontend && npx vitest run src/components/GitHubConnectionCard.test.ts` → FAIL (component not found).

- [ ] **Step 3: Add the type, the composable and the component.** The composable follows `useSettingsUsers.list` (`errorText`, `handleUnauthorized`, `'Network error'` on throw) and sets `loading` around the request.

- [ ] **Step 4: Place the card** in `SettingsPanel.vue`: `<GitHubConnectionCard v-if="subTab === 'general'" />` between `<ServiceZone>` and the access `<section>`. In `SettingsPanel.test.ts`, make the default handler answer `/api/release/github` with `{ mode: 'off', repo: 'percona/obs-packaging', ok: false, error: '', failed_step: '', checked_at: '2026-11-23T00:00:00Z' }`, and add:

```ts
it('shows the GitHub card on General only', async () => {
  const w = await mounted()
  expect(w.find('[data-testid="github-zone"]').exists()).toBe(true)
  await w.find('[data-testid="settings-tab-log"]').trigger('click')
  expect(w.find('[data-testid="github-zone"]').exists()).toBe(false)
})
```

The Server log sub-tab opens an `EventSource`; reuse the `FakeEventSource` stub the file already defines, as its existing log-tab tests do.

- [ ] **Step 5: Run** `cd frontend && npm test && npx vue-tsc --noEmit` → pass.

- [ ] **Step 6: Commit**

```bash
git add frontend/src
git commit -s -m "feat(settings): show the GitHub connection"
```

```json:metadata
{"files": ["frontend/src/types/release.ts", "frontend/src/composables/useGitHubConnection.ts", "frontend/src/components/GitHubConnectionCard.vue", "frontend/src/components/GitHubConnectionCard.test.ts", "frontend/src/components/SettingsPanel.vue", "frontend/src/components/SettingsPanel.test.ts"], "verifyCommand": "cd frontend && npm test && npx vue-tsc --noEmit", "acceptanceCriteria": ["four states render the specified copy", "Test connection refetches and is disabled in flight", "load error shown, button enabled", "card on General between Service and Access only", "npm test and vue-tsc pass"], "modelTier": "standard"}
```

---

### Task 6: Documentation

**Goal:** The operator documentation describes the App credentials, and the old spec points to the new one.

**Files:**
- Modify: `config.yaml.example` (release block, lines 119-128), `backend/config.yaml.example` (same block, if present)
- Modify: `.env.example`
- Modify: `README.md` (Release section, "To enable it", around line 99)
- Modify: `deploy/host/README.md` (the `env` block, around line 32)
- Modify: `docs/superpowers/specs/2026-11-11-release-tab-design.md` (§ 5.1)
- Modify: `docs/superpowers/specs/2026-11-23-github-app-release-auth-design.md` (status line)

**Acceptance Criteria:**
- [ ] Each of the four operator files names both variables, says the key is `base64 -w0` of the PEM on one line, and says the App pair wins over `RELEASE_GITHUB_TOKEN`, which remains as the fallback.
- [ ] `README.md` lists the required App permissions (`contents` write, `pull_requests` write, `actions` write, `checks` read, `metadata` read), says the installation ID is discovered, that actions appear as `<app-slug>[bot]`, and that a misconfigured pair does not fall back to the token.
- [ ] `config.yaml.example` keeps the key out of the file: `github_app_private_key: ""` with a comment to set it in the environment.
- [ ] The release-tab spec § 5.1 has one added sentence pointing to the new spec; the new spec's status reads `implemented`.
- [ ] No file under `deploy/host/` other than `README.md` changes.

**Verify:** `grep -l RELEASE_GITHUB_APP_PRIVATE_KEY config.yaml.example .env.example README.md deploy/host/README.md | wc -l` → `4`; `git diff --stat main -- deploy/host` lists only `README.md`

**Steps:**

- [ ] **Step 1: Edit the files** to meet the criteria. In `deploy/host/README.md` add the two lines to the `env` block as optional entries, with the comment on its own line (podman env files have no inline comments).

- [ ] **Step 2: Run the Verify commands** → `4`, and only `deploy/host/README.md`.

- [ ] **Step 3: Commit**

```bash
git add config.yaml.example backend/config.yaml.example .env.example README.md deploy/host/README.md docs/superpowers/specs
git commit -s -m "docs: GitHub App credentials for the Release tab"
```

```json:metadata
{"files": ["config.yaml.example", "backend/config.yaml.example", ".env.example", "README.md", "deploy/host/README.md", "docs/superpowers/specs/2026-11-11-release-tab-design.md", "docs/superpowers/specs/2026-11-23-github-app-release-auth-design.md"], "verifyCommand": "grep -l RELEASE_GITHUB_APP_PRIVATE_KEY config.yaml.example .env.example README.md deploy/host/README.md | wc -l", "acceptanceCriteria": ["four operator files name both variables, base64 -w0, precedence", "README lists permissions, discovered installation, bot attribution, no fallback on misconfiguration", "config.yaml.example keeps the key out of the file", "old spec points to the new one; new spec status implemented", "only README.md changes under deploy/host"], "modelTier": "mechanical"}
```

---

## Dependencies

Task 2 needs Task 1. Task 3 needs Task 2. Task 4 needs Task 3. Task 5 needs Task 4 only for the response shape, which this plan fixes, so it can run after Task 3 or in parallel with Task 4. Task 6 is last.

## Not in this plan

- Removing `RELEASE_GITHUB_TOKEN` support (spec non-goal).
- Checking the two open risks of spec § 9 against the real App (approving held runs, ETag behaviour across token changes). That needs a real release through the deployed App.
