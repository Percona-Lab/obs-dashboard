# Self-upgrade from the UI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Signed-in users can see newer GitHub releases of obs-dashboard in Settings and apply one. The dashboard writes a request file, and a host-side `apply.sh` pulls the pinned image, restarts it, health-checks it and rolls back on failure.

**Architecture:** A new Go package, `backend/internal/upgrade`, does four things:
- polls the GitHub releases API;
- keeps an `upgrades` table in SQLite;
- computes host-file requirements from each release's `### Host changes` notes;
- writes and reads the `apply-request` and `apply-status` handshake files.

The `api` package exposes it under the existing sign-in and Origin checks. A Vue `useUpgrades` singleton feeds three pieces of UI: a Settings "Service" zone, an update dot on the header, and a host-update banner. `deploy/host/` holds four files, tested in CI and shipped as a release asset:
- the Quadlet unit;
- `apply.sh`;
- the apply `.path` and `.service` units.

A tag-triggered `release.yml` publishes the image and the host-files tarball.

**Tech Stack:** Go 1.25 (chi, modernc SQLite), Vue 3 + TypeScript + Tailwind, Vitest, bash + jq, systemd and Podman Quadlet, GitHub Actions.

**Spec:** `docs/superpowers/specs/2026-11-06-self-upgrade-design.md`

## Global Constraints

- **Public repo.**
  - Never write the names of private repositories, private tooling or internal hostnames anywhere in this repo: code, docs, commits, the changelog or test fixtures.
  - Deployment is described only through the tool-agnostic host contract (spec §4) and `deploy/host/README.md`.
- **Commits:** always `git commit -s`, and never add a `Co-Authored-By:` trailer.
- **Go:**
  - The module is `github.com/percona/obs-dashboard`, and `backend/go.mod` stays at `go 1.25.0`.
  - Add no new Go or npm dependencies.
  - Bind SQLite timestamps as strings (`time.RFC3339Nano`), never as raw `time.Time`.
- **Versions** are plain `vX.Y.Z`. Every validation in Go, bash and the scripts uses the regex `^v[0-9]+\.[0-9]+\.[0-9]+$`.
- **Image:** `ghcr.io/percona-lab/obs-dashboard:<tag>`. The default `upgrades.github_repo` is `Percona-Lab/obs-dashboard`.
- **Host contract, verbatim from spec §4:**
  - Files: the `apply-request` and `apply-status` JSON field names; the backup name regex `^pre-v[0-9]+\.[0-9]+\.[0-9]+-[0-9]{8}T[0-9]{6}Z\.db$`; `/healthz` returning `{"status":"ok","version":"…"}`.
  - Paths: `/etc/obs-dashboard/{env,config.yaml,apply/}`, `/var/lib/obs-dashboard` (mounted at `/data`), `/usr/local/lib/obs-dashboard/apply.sh`, and the drop-in `/etc/containers/systemd/obs-dashboard.container.d/10-version.conf`.
  - Env: `UPGRADE_APPLY_DIR` and `OBS_DASHBOARD_HOST_FILES_VERSION`.
- **Access:** upgrade API routes require a signed-in user (`RequireUser`). The POST routes also require `CheckOrigin`.
- **Security in `apply.sh`:** the image repository is fixed in the script and never read from the request. Every request field is validated before use.

**User decisions (already made):**
- Delivery uses approval-gated upgrades: CI publishes a pinned image, the dashboard only writes a request, and a root host script applies it. No container-runtime socket is used.
- The repo is `Percona-Lab/obs-dashboard` and is public, so no GitHub token is needed.
- The runtime is Podman Quadlet.
- The host layout is `/etc/obs-dashboard` plus `/var/lib/obs-dashboard`, with the root `apply.sh` triggered by a systemd path unit.
- The Quadlet unit, `apply.sh` and the apply units live and are tested in this repo, and ship as a checksummed release tarball. Site files (`env`, `config.yaml`, proxy, TLS) belong to the deployment.
- Rollback is automatic only, on a failed pull or health check, and restores the pin and the DB backup. The UI never offers downgrades.
- There is no drain step. Any signed-in, allowlisted user may check for and apply upgrades.
- Host changes are announced in a `### Host changes` changelog subsection. The dashboard parses them from the release notes. It shows a non-dismissible banner with tool-agnostic manual steps and refuses blocked releases.
- The banner and docs must not name any deployment tool.

**Plan rulings (where the spec is silent or tight; the cost if wrong is small):**
- `Apply` accepts rows in status `available` **or `failed`**, so a failed upgrade can be retried. The spec lists only `available`, but without this a newer failed release could never be retried. Cost if wrong: one condition to remove.
- `GET /api/upgrades` adds two fields:
  - `repo` (the configured `upgrades.github_repo`), which the banner needs for its download URLs;
  - `checked_at`, the time of the last check, which the UI shows.

  Both additions are additive.
- `Reconcile` applies a status file to the row named by `status.version` when that row is `applying`. Otherwise it applies the status to whichever row is `applying`. This means a status with an empty or invalid version still releases the in-flight lock.
- The frontend/API split in `main.go` moves into `api.WithFrontend`, so `/healthz` is routed to the router and the routing can be tested.

## Review Focus

These are failure modes the spec implies but doesn't spell out. Each is pinned by a test in the owning task.
1. **GitHub bodies with CRLF line endings.** `HostChanges` must still find `### Host changes` when lines end in `\r\n`, which GitHub often stores. Test in Task 1.
2. **A status file whose `version` is empty or doesn't match.** The in-flight `applying` row must still be resolved. Otherwise every later apply returns 409 forever. Test in Task 3.
3. **`/healthz` behind the SPA file server.** With `FRONTEND_DIR` set, `/healthz` must reach the router and not return `index.html` or 404. Otherwise every upgrade's health check fails and rolls back. Test in Task 4.
4. **First upgrade with no `backups/` directory yet.** `Apply` must create `<dbdir>/backups`. If the directory can't be created, it fails cleanly and writes no request. Test in Task 3.
5. **Polling while the reverse proxy returns an HTML 502 or 503 during the restart.** The UI must show "Restarting…" and keep polling, not error out on JSON parsing. Test in Task 5.

---

## File Structure

| Path | Responsibility |
|---|---|
| `backend/internal/version/version.go` | Build version (`-ldflags -X`) |
| `backend/internal/upgrade/semver.go` | `vX.Y.Z` validation and comparison |
| `backend/internal/upgrade/github.go` | GitHub release source and `### Host changes` extraction |
| `backend/internal/upgrade/store.go` | `upgrades` table access |
| `backend/internal/upgrade/files.go` | Atomic request write, status read |
| `backend/internal/upgrade/manager.go` | Check, view, apply (backup + request), reconcile, run loop |
| `backend/internal/store/db.go` | Adds the `upgrades` table to the schema |
| `backend/internal/config/config.go` | `Upgrades` config section |
| `backend/internal/api/health.go` | `GET /healthz` |
| `backend/internal/api/frontend.go` | `WithFrontend` (SPA versus router split) |
| `backend/internal/api/upgrades.go` | Upgrade HTTP handlers |
| `backend/internal/api/server.go` | Route registration (new `upg` param) |
| `backend/cmd/obsboard/main.go` | Wiring |
| `Dockerfile` | `ARG VERSION`, ldflags |
| `frontend/src/composables/http.ts` | Shared `errorText` |
| `frontend/src/composables/useUpgrades.ts` | Upgrades state singleton, API calls, host-update commands |
| `frontend/src/components/HostUpdateSteps.vue` | Host instructions + manual steps (shared) |
| `frontend/src/components/ServiceZone.vue` | Settings "Service" zone |
| `frontend/src/components/HostUpdateBanner.vue` | Global amber banner |
| `frontend/src/components/SettingsPanel.vue`, `AppHeader.vue`, `App.vue` | Mount the above |
| `deploy/host/obs-dashboard.container` | Static Quadlet unit |
| `deploy/host/obs-dashboard-apply.path`, `.service` | Apply trigger units |
| `deploy/host/apply.sh` | Host-side upgrade + rollback |
| `deploy/host/README.md` | Manual install/update/rollback guide |
| `deploy/host/test/run.sh`, `test/stubs/*`, `test/quadlet-check.sh` | Host-script tests |
| `scripts/changelog-section.sh`, `scripts/check-release.sh`, `scripts/release.sh`, `scripts/test/run.sh` | Release tooling and its tests |
| `CHANGELOG.md`, `.github/workflows/release.yml`, `.github/workflows/ci.yml`, `Taskfile.yml` | Release process |

---

### Task 0: Version stamp, `/healthz`, frontend split

**Goal:** The binary carries a build version that `/healthz` reports, and `/healthz` stays reachable when the SPA is served.

**Files:**
- Create: `backend/internal/version/version.go`
- Create: `backend/internal/api/health.go`
- Create: `backend/internal/api/frontend.go`
- Create: `backend/internal/api/health_test.go`
- Modify: `backend/internal/api/server.go` (register `/healthz`)
- Modify: `backend/cmd/obsboard/main.go` (log version, use `api.WithFrontend`)
- Modify: `Dockerfile` (`ARG VERSION`, ldflags)

**Acceptance Criteria:**
- [ ] `GET /healthz` returns 200 with `{"status":"ok","version":"<version.Version>"}`.
- [ ] With a frontend dir, `GET /healthz` is served by the router, `GET /` serves `index.html`, and `/api/*` and `/auth/*` still reach the router.
- [ ] `go build -ldflags "-X github.com/percona/obs-dashboard/internal/version.Version=v9.9.9"` embeds `v9.9.9`.
- [ ] `Dockerfile` accepts `--build-arg VERSION=…`, defaulting to `dev`.

**Verify:** `cd backend && go test ./internal/api/ -run 'TestHealthz|TestWithFrontend' -v` → PASS

**Steps:**

- [ ] **Step 1: Write the failing tests** in `backend/internal/api/health_test.go`:

```go
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/percona/obs-dashboard/internal/version"
)

func TestHealthz(t *testing.T) {
	old := version.Version
	version.Version = "v1.2.3"
	t.Cleanup(func() { version.Version = old })

	rec := httptest.NewRecorder()
	setupTestServer(t).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "ok" || body["version"] != "v1.2.3" {
		t.Fatalf("body %v", body)
	}
}

func TestWithFrontend(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>spa</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	router := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("router")) })
	h := WithFrontend(router, dir)
	for path, want := range map[string]string{
		"/healthz":       "router",
		"/api/overview":  "router",
		"/auth/login":    "router",
		"/":              "spa",
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("%s: got %q, want %q", path, rec.Body.String(), want)
		}
	}
	if WithFrontend(router, "") == nil {
		t.Fatal("empty dir must return the router")
	}
}
```

- [ ] **Step 2: Run the tests to confirm they fail**

Run: `cd backend && go test ./internal/api/ -run 'TestHealthz|TestWithFrontend'`
Expected: a build failure (`undefined: WithFrontend`, missing package `version`).

- [ ] **Step 3: Implement**

`backend/internal/version/version.go`:
```go
// Package version holds the build version. Release images stamp it with
// -ldflags "-X github.com/percona/obs-dashboard/internal/version.Version=vX.Y.Z".
package version

// Version is "dev" for local builds.
var Version = "dev"
```

`backend/internal/api/health.go`:
```go
package api

import (
	"encoding/json"
	"net/http"

	"github.com/percona/obs-dashboard/internal/version"
)

// healthzHandler is the liveness probe the host apply script polls: an
// upgrade succeeds only when it reports the requested version.
func healthzHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "version": version.Version})
}
```

`backend/internal/api/frontend.go`:
```go
package api

import (
	"net/http"
	"strings"
)

// WithFrontend serves the built SPA from dir and sends API, auth and health
// paths to router. An empty dir returns router unchanged.
func WithFrontend(router http.Handler, dir string) http.Handler {
	if dir == "" {
		return router
	}
	fs := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if strings.HasPrefix(p, "/api") || strings.HasPrefix(p, "/auth/") || p == "/healthz" {
			router.ServeHTTP(w, r)
			return
		}
		fs.ServeHTTP(w, r)
	})
}
```

In `backend/internal/api/server.go`, right after `authSvc.Mount(r)`, add:
```go
	r.Get("/healthz", healthzHandler)
```

In `backend/cmd/obsboard/main.go`:
- Add the import `"github.com/percona/obs-dashboard/internal/version"`.
- Make the first statement of `run()`:
  ```go
  	slog.Info("obs-dashboard starting", "version", version.Version)
  ```
- Replace the whole `var handler http.Handler = router` / `if cfg.Server.FrontendDir != "" { … }` block with:
  ```go
  	handler := api.WithFrontend(router, cfg.Server.FrontendDir)
  ```
- Remove the `strings` import if nothing else uses it (`go build` will tell you).

In `Dockerfile`, Stage 2, replace the `RUN CGO_ENABLED=0 go build …` line with:
```dockerfile
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -ldflags "-X github.com/percona/obs-dashboard/internal/version.Version=${VERSION}" -o /obsboard ./cmd/obsboard
```

- [ ] **Step 4: Run the tests and a stamped build**

Run: `cd backend && go test ./internal/api/ -run 'TestHealthz|TestWithFrontend' -v && go vet ./... && go build -ldflags "-X github.com/percona/obs-dashboard/internal/version.Version=v9.9.9" -o /tmp/obsboard-ver ./cmd/obsboard && grep -c v9.9.9 /tmp/obsboard-ver`
Expected: both tests PASS, and the grep count is ≥ 1.

- [ ] **Step 5: Run the full backend suite and commit**

Run: `cd backend && go test ./...` → all ok.
```bash
git add backend/internal/version backend/internal/api/health.go backend/internal/api/frontend.go backend/internal/api/health_test.go backend/internal/api/server.go backend/cmd/obsboard/main.go Dockerfile
git commit -s -m "feat: build version stamp and /healthz"
```

---

### Task 1: Version comparison and GitHub release source

**Goal:** The dashboard can list valid `vX.Y.Z` GitHub releases and extract each one's `### Host changes` text.

**Files:**
- Create: `backend/internal/upgrade/semver.go`
- Create: `backend/internal/upgrade/github.go`
- Test: `backend/internal/upgrade/semver_test.go`, `backend/internal/upgrade/github_test.go`

**Acceptance Criteria:**
- [ ] `ValidVersion` accepts only `^v\d+\.\d+\.\d+$`. `Newer(a, b)` is a strict, numeric comparison, and invalid input is never newer.
- [ ] `GitHubSource.Releases` skips drafts, prereleases, releases without `published_at`, and invalid tags. It sends `Accept: application/vnd.github+json` and returns an error on non-200 responses.
- [ ] `HostChanges` returns the trimmed body under a heading level 2–4 titled "Host changes" (case-insensitive), up to the next heading of the same or a higher level. It returns `""` when there is none, and it handles CRLF.

**Verify:** `cd backend && go test ./internal/upgrade/ -v` → PASS

**Steps:**

- [ ] **Step 1: Write the failing tests**

`backend/internal/upgrade/semver_test.go`:
```go
package upgrade

import "testing"

func TestValidVersion(t *testing.T) {
	for s, want := range map[string]bool{
		"v0.1.0": true, "v10.20.30": true, "0.1.0": false, "v0.1": false,
		"v0.1.0-rc1": false, "dev": false, "": false, "v0.1.0 ": false,
		"v99999999999999999999.0.0": false,
	} {
		if got := ValidVersion(s); got != want {
			t.Errorf("ValidVersion(%q) = %v, want %v", s, got, want)
		}
	}
}

func TestNewer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"v0.2.0", "v0.1.0", true},
		{"v0.10.0", "v0.9.0", true},
		{"v1.0.0", "v0.99.99", true},
		{"v0.1.0", "v0.1.0", false},
		{"v0.1.0", "v0.2.0", false},
		{"v0.2.0", "dev", false},
		{"dev", "v0.1.0", false},
	}
	for _, c := range cases {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
```

`backend/internal/upgrade/github_test.go`:
```go
package upgrade

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

const releasesJSON = `[
 {"tag_name":"v0.3.0","body":"## Added\n- x\n\n### Host changes\n\nAdd FOO=1 to env.\n\n### Fixed\n- y","html_url":"https://h/v0.3.0","draft":false,"prerelease":false,"published_at":"2026-11-03T10:00:00Z"},
 {"tag_name":"v0.2.0","body":"notes","html_url":"https://h/v0.2.0","draft":false,"prerelease":false,"published_at":"2026-11-02T10:00:00Z"},
 {"tag_name":"v0.4.0","body":"","html_url":"","draft":true,"prerelease":false,"published_at":null},
 {"tag_name":"v0.5.0-rc1","body":"","html_url":"","draft":false,"prerelease":true,"published_at":"2026-11-04T10:00:00Z"},
 {"tag_name":"latest","body":"","html_url":"","draft":false,"prerelease":false,"published_at":"2026-11-04T10:00:00Z"},
 {"tag_name":"v0.6.0","body":"","html_url":"","draft":false,"prerelease":false,"published_at":null}
]`

func TestGitHubSourceFiltersReleases(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/Percona-Lab/obs-dashboard/releases" || r.URL.Query().Get("per_page") != "30" {
			t.Errorf("unexpected request %s", r.URL)
		}
		if r.Header.Get("Accept") != "application/vnd.github+json" {
			t.Errorf("Accept = %q", r.Header.Get("Accept"))
		}
		_, _ = w.Write([]byte(releasesJSON))
	}))
	defer srv.Close()

	rels, err := GitHubSource{APIBase: srv.URL, Repo: "Percona-Lab/obs-dashboard"}.Releases(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rels) != 2 || rels[0].Version != "v0.3.0" || rels[1].Version != "v0.2.0" {
		t.Fatalf("releases = %+v", rels)
	}
	if rels[0].HostChanges != "Add FOO=1 to env." || rels[0].URL != "https://h/v0.3.0" || rels[0].PublishedAt.IsZero() {
		t.Fatalf("v0.3.0 = %+v", rels[0])
	}
	if rels[1].HostChanges != "" {
		t.Fatalf("v0.2.0 host changes = %q", rels[1].HostChanges)
	}
}

func TestGitHubSourceHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "rate limited", http.StatusForbidden)
	}))
	defer srv.Close()
	if _, err := (GitHubSource{APIBase: srv.URL, Repo: "a/b"}).Releases(context.Background()); err == nil {
		t.Fatal("want error on 403")
	}
}

func TestHostChanges(t *testing.T) {
	cases := map[string]struct{ in, want string }{
		"absent":         {"## Added\n- x", ""},
		"h3":             {"### Host changes\nnew env\n### Fixed\n- y", "new env"},
		"h2 last":        {"## Host changes\n\nline1\nline2\n", "line1\nline2"},
		"h4 mixed case":  {"#### HOST Changes\nA\n#### Other\nB", "A"},
		"deeper kept":    {"### Host changes\nA\n#### Detail\nB\n## Next\nC", "A\n#### Detail\nB"},
		"crlf":           {"### Host changes\r\nAdd BAR\r\n### Fixed\r\n- y\r\n", "Add BAR"},
		"h1 not matched": {"# Host changes\nA", ""},
		"h5 not matched": {"##### Host changes\nA", ""},
	}
	for name, c := range cases {
		if got := HostChanges(c.in); got != c.want {
			t.Errorf("%s: HostChanges = %q, want %q", name, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run the tests to confirm they fail**

Run: `cd backend && go test ./internal/upgrade/`
Expected: a build failure (the package has no non-test files).

- [ ] **Step 3: Implement**

`backend/internal/upgrade/semver.go`:
```go
// Package upgrade lists newer releases and hands approved upgrades to the
// host through the apply-request/apply-status file contract.
package upgrade

import (
	"regexp"
	"strconv"
)

var versionRe = regexp.MustCompile(`^v([0-9]+)\.([0-9]+)\.([0-9]+)$`)

type semver [3]int

func parseVersion(s string) (semver, bool) {
	m := versionRe.FindStringSubmatch(s)
	if m == nil {
		return semver{}, false
	}
	var v semver
	for i := range v {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return semver{}, false
		}
		v[i] = n
	}
	return v, true
}

// ValidVersion reports whether s is a plain vX.Y.Z tag.
func ValidVersion(s string) bool {
	_, ok := parseVersion(s)
	return ok
}

// Newer reports whether a is a strictly higher vX.Y.Z than b. Invalid input
// (including "dev") is never newer and nothing is newer than it.
func Newer(a, b string) bool {
	va, ok1 := parseVersion(a)
	vb, ok2 := parseVersion(b)
	if !ok1 || !ok2 {
		return false
	}
	for i := range va {
		if va[i] != vb[i] {
			return va[i] > vb[i]
		}
	}
	return false
}
```

`backend/internal/upgrade/github.go`:
```go
package upgrade

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Release is one published release as the dashboard sees it.
type Release struct {
	Version     string
	Notes       string
	URL         string
	PublishedAt time.Time
	HostChanges string
}

// ReleaseSource lists published releases.
type ReleaseSource interface {
	Releases(ctx context.Context) ([]Release, error)
}

// GitHubSource reads releases anonymously from the GitHub REST API.
type GitHubSource struct {
	APIBase string // default https://api.github.com
	Repo    string // owner/name
	Client  *http.Client
}

// Releases returns published, non-prerelease vX.Y.Z releases.
func (g GitHubSource) Releases(ctx context.Context) ([]Release, error) {
	base := g.APIBase
	if base == "" {
		base = "https://api.github.com"
	}
	client := g.Client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/repos/"+g.Repo+"/releases?per_page=30", nil)
	if err != nil {
		return nil, fmt.Errorf("github releases: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github releases: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github releases: HTTP %d", resp.StatusCode)
	}
	var raw []struct {
		TagName     string     `json:"tag_name"`
		Body        string     `json:"body"`
		HTMLURL     string     `json:"html_url"`
		Draft       bool       `json:"draft"`
		Prerelease  bool       `json:"prerelease"`
		PublishedAt *time.Time `json:"published_at"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&raw); err != nil {
		return nil, fmt.Errorf("github releases: decode: %w", err)
	}
	out := make([]Release, 0, len(raw))
	for _, r := range raw {
		if r.Draft || r.Prerelease || r.PublishedAt == nil || !ValidVersion(r.TagName) {
			continue
		}
		out = append(out, Release{
			Version:     r.TagName,
			Notes:       r.Body,
			URL:         r.HTMLURL,
			PublishedAt: r.PublishedAt.UTC(),
			HostChanges: HostChanges(r.Body),
		})
	}
	return out, nil
}

var headingRe = regexp.MustCompile(`^(#{1,6})\s+(.*?)\s*#*\s*$`)

// HostChanges returns the body of the "Host changes" subsection (heading
// level 2-4, case-insensitive) up to the next heading of the same or a
// higher level, trimmed. Empty when absent.
func HostChanges(notes string) string {
	lines := strings.Split(strings.ReplaceAll(notes, "\r\n", "\n"), "\n")
	level := 0
	var out []string
	for _, l := range lines {
		m := headingRe.FindStringSubmatch(l)
		if level == 0 {
			if m != nil && len(m[1]) >= 2 && len(m[1]) <= 4 && strings.EqualFold(m[2], "Host changes") {
				level = len(m[1])
			}
			continue
		}
		if m != nil && len(m[1]) <= level {
			break
		}
		out = append(out, l)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}
```

- [ ] **Step 4: Run the tests**

Run: `cd backend && go test ./internal/upgrade/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**
```bash
git add backend/internal/upgrade
git commit -s -m "feat(upgrade): GitHub release source and version comparison"
```

---

### Task 2: `upgrades` table and store

**Goal:** Releases and upgrade state persist in SQLite, under the status rules from spec §2.

**Files:**
- Modify: `backend/internal/store/db.go` (add the table to `schema`, after the `sessions` index)
- Create: `backend/internal/upgrade/store.go`
- Test: `backend/internal/upgrade/store_test.go`

**Acceptance Criteria:**
- [ ] `store.Open` creates `upgrades`, with the exact columns listed in the spec.
- [ ] `upsert` inserts new versions as `available`. On a conflict it refreshes `notes`, `url`, `host_changes` and `published_at`, keeps `applying`, `applied` and `failed` as they are, and revives `superseded` to `available`.
- [ ] `all()` returns rows sorted newest-first by version, not lexically: `v0.10.0` sorts before `v0.9.0`.
- [ ] `markApplying` and `markFinished` round-trip their timestamps, which are stored as RFC3339Nano strings.

**Verify:** `cd backend && go test ./internal/upgrade/ -run TestStore -v` → PASS

**Steps:**

- [ ] **Step 1: Add the table.** In `backend/internal/store/db.go`, append this inside the `schema` string, after `CREATE INDEX IF NOT EXISTS sessions_email ON sessions (email);`:

```sql

CREATE TABLE IF NOT EXISTS upgrades (
    version       TEXT PRIMARY KEY,
    notes         TEXT NOT NULL,
    url           TEXT NOT NULL,
    host_changes  TEXT NOT NULL DEFAULT '',
    published_at  TEXT NOT NULL,
    status        TEXT NOT NULL,
    requested_by  TEXT,
    requested_at  TEXT,
    finished_at   TEXT,
    error         TEXT
);
```

- [ ] **Step 2: Write the failing test** `backend/internal/upgrade/store_test.go`:

```go
package upgrade

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/percona/obs-dashboard/internal/store"
)

func openTestDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "obsboard.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db, path
}

func rel(v, host string) Release {
	return Release{Version: v, Notes: "notes " + v, URL: "https://h/" + v, PublishedAt: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), HostChanges: host}
}

func TestStoreUpsertAndOrder(t *testing.T) {
	db, _ := openTestDB(t)
	s := store_{db}
	for _, v := range []string{"v0.9.0", "v0.10.0", "v0.2.0"} {
		if err := s.upsert(rel(v, "")); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.all()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || rows[0].Version != "v0.10.0" || rows[1].Version != "v0.9.0" || rows[2].Version != "v0.2.0" {
		t.Fatalf("order: %v", versions(rows))
	}
	if rows[0].Status != StatusAvailable {
		t.Fatalf("status %q", rows[0].Status)
	}
}

func TestStoreUpsertKeepsStatusAndRevivesSuperseded(t *testing.T) {
	db, _ := openTestDB(t)
	s := store_{db}
	now := time.Date(2026, 11, 6, 12, 0, 0, 123, time.UTC)
	_ = s.upsert(rel("v0.2.0", ""))
	_ = s.upsert(rel("v0.3.0", ""))
	if err := s.markApplying("v0.2.0", "a@percona.com", now); err != nil {
		t.Fatal(err)
	}
	if err := s.setStatus("v0.3.0", StatusSuperseded); err != nil {
		t.Fatal(err)
	}
	r2 := rel("v0.2.0", "new host text")
	r2.Notes = "edited"
	_ = s.upsert(r2)
	_ = s.upsert(rel("v0.3.0", ""))
	rows, _ := s.all()
	byV := map[string]Row{}
	for _, r := range rows {
		byV[r.Version] = r
	}
	if r := byV["v0.2.0"]; r.Status != StatusApplying || r.Notes != "edited" || r.HostChanges != "new host text" || r.RequestedBy != "a@percona.com" || r.RequestedAt == nil || !r.RequestedAt.Equal(now) {
		t.Fatalf("v0.2.0 = %+v", r)
	}
	if byV["v0.3.0"].Status != StatusAvailable {
		t.Fatalf("superseded not revived: %q", byV["v0.3.0"].Status)
	}
}

func TestStoreMarkFinished(t *testing.T) {
	db, _ := openTestDB(t)
	s := store_{db}
	_ = s.upsert(rel("v0.2.0", ""))
	at := time.Date(2026, 11, 6, 12, 3, 0, 0, time.UTC)
	_ = s.markApplying("v0.2.0", "a@percona.com", at)
	if err := s.markFinished("v0.2.0", false, "boom", at); err != nil {
		t.Fatal(err)
	}
	v, err := s.applying()
	if err != nil || v != "" {
		t.Fatalf("applying = %q, %v", v, err)
	}
	rows, _ := s.all()
	if rows[0].Status != StatusFailed || rows[0].Error != "boom" || rows[0].FinishedAt == nil || !rows[0].FinishedAt.Equal(at) {
		t.Fatalf("row = %+v", rows[0])
	}
}

func versions(rows []Row) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Version
	}
	return out
}
```

- [ ] **Step 3: Run the test to confirm it fails**

Run: `cd backend && go test ./internal/upgrade/ -run TestStore`
Expected: a build failure (`undefined: store_`).

- [ ] **Step 4: Implement** `backend/internal/upgrade/store.go`. The type is named `store_` because the `store` package is imported in tests.

```go
package upgrade

import (
	"database/sql"
	"errors"
	"sort"
	"time"
)

// Upgrade row statuses.
const (
	StatusAvailable  = "available"
	StatusApplying   = "applying"
	StatusApplied    = "applied"
	StatusFailed     = "failed"
	StatusSuperseded = "superseded"
)

// Row is a release plus its upgrade state.
type Row struct {
	Release
	Status      string
	RequestedBy string
	RequestedAt *time.Time
	FinishedAt  *time.Time
	Error       string
}

type store_ struct{ db *sql.DB }

func fmtTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func parseTime(ns sql.NullString) *time.Time {
	if !ns.Valid || ns.String == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339Nano, ns.String)
	if err != nil {
		return nil
	}
	return &t
}

// upsert inserts r as available, or refreshes its release fields. Statuses
// are kept, except superseded, which becomes available again (r is newer
// than the running version again, e.g. after a manual rollback).
func (s store_) upsert(r Release) error {
	_, err := s.db.Exec(`INSERT INTO upgrades (version, notes, url, host_changes, published_at, status)
VALUES (?, ?, ?, ?, ?, 'available')
ON CONFLICT(version) DO UPDATE SET
  notes = excluded.notes,
  url = excluded.url,
  host_changes = excluded.host_changes,
  published_at = excluded.published_at,
  status = CASE WHEN upgrades.status = 'superseded' THEN 'available' ELSE upgrades.status END`,
		r.Version, r.Notes, r.URL, r.HostChanges, fmtTime(r.PublishedAt))
	return err
}

// all returns every row, newest version first.
func (s store_) all() ([]Row, error) {
	rows, err := s.db.Query(`SELECT version, notes, url, host_changes, published_at, status,
  requested_by, requested_at, finished_at, error FROM upgrades`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Row
	for rows.Next() {
		var r Row
		var published string
		var by, reqAt, finAt, errText sql.NullString
		if err := rows.Scan(&r.Version, &r.Notes, &r.URL, &r.HostChanges, &published, &r.Status, &by, &reqAt, &finAt, &errText); err != nil {
			return nil, err
		}
		if t, err := time.Parse(time.RFC3339Nano, published); err == nil {
			r.PublishedAt = t
		}
		r.RequestedBy = by.String
		r.RequestedAt = parseTime(reqAt)
		r.FinishedAt = parseTime(finAt)
		r.Error = errText.String
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool { return Newer(out[i].Version, out[j].Version) })
	return out, nil
}

func (s store_) setStatus(version, status string) error {
	_, err := s.db.Exec(`UPDATE upgrades SET status = ? WHERE version = ?`, status, version)
	return err
}

func (s store_) markApplying(version, by string, at time.Time) error {
	_, err := s.db.Exec(`UPDATE upgrades SET status = 'applying', requested_by = ?, requested_at = ?,
  finished_at = NULL, error = NULL WHERE version = ?`, by, fmtTime(at), version)
	return err
}

func (s store_) markFinished(version string, ok bool, errText string, at time.Time) error {
	status := StatusFailed
	if ok {
		status = StatusApplied
	}
	_, err := s.db.Exec(`UPDATE upgrades SET status = ?, error = ?, finished_at = ? WHERE version = ?`,
		status, errText, fmtTime(at), version)
	return err
}

// applying returns the version currently being applied, or "".
func (s store_) applying() (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT version FROM upgrades WHERE status = 'applying' LIMIT 1`).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}
```

- [ ] **Step 5: Run the tests and commit**

Run: `cd backend && go test ./internal/upgrade/ ./internal/store/ -v 2>&1 | tail -5` → ok.
```bash
git add backend/internal/store/db.go backend/internal/upgrade/store.go backend/internal/upgrade/store_test.go
git commit -s -m "feat(upgrade): upgrades table and store"
```

---

### Task 3: Upgrade manager (check, view, apply, reconcile)

**Goal:** A `Manager` that implements spec §2: release checks, host requirements, apply (DB backup plus request file), reconciliation of the status file, and the run loop.

**Files:**
- Create: `backend/internal/upgrade/files.go`
- Create: `backend/internal/upgrade/manager.go`
- Test: `backend/internal/upgrade/manager_test.go`

**Acceptance Criteria:**
- [ ] `Check` behaves like this:
  - It upserts releases newer than `Current`, or all releases when `Current` is not a valid version (for example `dev`).
  - It supersedes `available` and `failed` rows that are no longer newer.
  - It never changes `applying` or `applied` rows.
  - It sets `checked_at`.
  - On a source error it returns the error and leaves state unchanged.
- [ ] `View` returns `available` and `applying` rows, plus at most 10 `applied` or `failed` rows. It also returns `in_flight`, `repo`, `host_files_version` (`""` when invalid), `apply_enabled`, and per-row `requires_host`, `blocked` and `host_instructions`, oldest first and never `nil`.
- [ ] `Apply` returns:
  - `ErrDisabled` when there is no `ApplyDir` or `Current` is invalid;
  - `ErrConflict` when another upgrade is in flight, the version is not newer, or the status is not `available` or `failed`;
  - `ErrNotFound` for an unknown version;
  - a `*HostUpdateError` (`errors.Is(…, ErrHostUpdateRequired)`) when the release is blocked.
- [ ] `Apply` success path:
  1. Creates `<dbdir>/backups/pre-<v>-<YYYYMMDDTHHMMSSZ>.db` with `VACUUM INTO`, after creating the directory if needed.
  2. Prunes the backups to the newest 5.
  3. Writes `apply-request` atomically with mode 0644 and exactly the fields `version, previous, backup, requested_by, requested_at`.
  4. Marks the row `applying`.

  If the backup fails, no request is written and the row is unchanged.
- [ ] `Reconcile`:
  - **Valid status file:** marks the matching `applying` row `applied` or `failed`, falling back to whichever row is `applying`, then deletes the file.
  - **Malformed status file:** leaves the file and the row.
  - **At startup:** marks an `applying` row with neither a request nor a status file `failed` with error `interrupted`.
- [ ] `ValidateApplyDir` rejects a missing path, a non-directory, and an unwritable directory.

**Verify:** `cd backend && go test ./internal/upgrade/ -v` → PASS

**Steps:**

- [ ] **Step 1: Write the failing tests** `backend/internal/upgrade/manager_test.go`:

```go
package upgrade

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

type fakeSource struct {
	rels []Release
	err  error
}

func (f *fakeSource) Releases(context.Context) ([]Release, error) { return f.rels, f.err }

var fixedNow = time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

type harness struct {
	m        *Manager
	src      *fakeSource
	applyDir string
	dbPath   string
}

func newHarness(t *testing.T, current string, mod func(*Options)) harness {
	t.Helper()
	db, path := openTestDB(t)
	applyDir := t.TempDir()
	src := &fakeSource{}
	o := Options{DB: db, DBPath: path, Source: src, Current: current, ApplyDir: applyDir,
		Repo: "Percona-Lab/obs-dashboard", Now: func() time.Time { return fixedNow }}
	if mod != nil {
		mod(&o)
	}
	return harness{m: NewManager(o), src: src, applyDir: applyDir, dbPath: path}
}

func (h harness) view(t *testing.T) View {
	t.Helper()
	v, err := h.m.View()
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func viewVersions(v View) []string {
	out := []string{}
	for _, r := range v.Releases {
		out = append(out, r.Version+":"+r.Status)
	}
	return out
}

func TestCheckListsOnlyNewer(t *testing.T) {
	h := newHarness(t, "v0.2.0", nil)
	h.src.rels = []Release{rel("v0.1.0", ""), rel("v0.2.0", ""), rel("v0.4.0", ""), rel("v0.3.0", "")}
	if err := h.m.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	v := h.view(t)
	if got := strings.Join(viewVersions(v), ","); got != "v0.4.0:available,v0.3.0:available" {
		t.Fatalf("releases = %s", got)
	}
	if v.CheckedAt == nil || !v.CheckedAt.Equal(fixedNow) || v.Current != "v0.2.0" || !v.ApplyEnabled || v.Repo != "Percona-Lab/obs-dashboard" {
		t.Fatalf("view = %+v", v)
	}
}

func TestCheckDevListsAllButCannotApply(t *testing.T) {
	h := newHarness(t, "dev", nil)
	h.src.rels = []Release{rel("v0.1.0", ""), rel("v0.2.0", "")}
	_ = h.m.Check(context.Background())
	v := h.view(t)
	if len(v.Releases) != 2 || v.ApplyEnabled {
		t.Fatalf("view = %+v", v)
	}
	if _, err := h.m.Apply(context.Background(), "v0.2.0", "a@percona.com"); !errors.Is(err, ErrDisabled) {
		t.Fatalf("err = %v", err)
	}
}

func TestCheckErrorKeepsState(t *testing.T) {
	h := newHarness(t, "v0.1.0", nil)
	h.src.rels = []Release{rel("v0.2.0", "")}
	_ = h.m.Check(context.Background())
	h.src.err = errors.New("github down")
	if err := h.m.Check(context.Background()); err == nil {
		t.Fatal("want error")
	}
	if len(h.view(t).Releases) != 1 {
		t.Fatal("state lost")
	}
}

func TestCheckSupersedesCaughtUpRows(t *testing.T) {
	h := newHarness(t, "v0.1.0", nil)
	h.src.rels = []Release{rel("v0.2.0", ""), rel("v0.3.0", "")}
	_ = h.m.Check(context.Background())
	_ = h.m.st.markFinished("v0.3.0", false, "x", fixedNow) // failed, still newer
	// Restart on v0.2.0 with the same DB.
	h2 := NewManager(Options{DB: h.m.o.DB, DBPath: h.dbPath, Source: h.src, Current: "v0.2.0", ApplyDir: h.applyDir, Now: h.m.o.Now})
	_ = h2.Check(context.Background())
	got := strings.Join(viewVersions(mustView(t, h2)), ",")
	if got != "v0.3.0:failed" {
		t.Fatalf("releases = %s", got)
	}
}

func mustView(t *testing.T, m *Manager) View {
	t.Helper()
	v, err := m.View()
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestCheckKeepsApplyingStatus(t *testing.T) {
	h := newHarness(t, "v0.1.0", nil)
	h.src.rels = []Release{rel("v0.2.0", "")}
	_ = h.m.Check(context.Background())
	if _, err := h.m.Apply(context.Background(), "v0.2.0", "a@percona.com"); err != nil {
		t.Fatal(err)
	}
	_ = h.m.Check(context.Background())
	v := h.view(t)
	if v.InFlight != "v0.2.0" || v.Releases[0].Status != StatusApplying {
		t.Fatalf("view = %+v", v)
	}
}

var backupRe = regexp.MustCompile(`^pre-v[0-9]+\.[0-9]+\.[0-9]+-[0-9]{8}T[0-9]{6}Z\.db$`)

func TestApplyWritesBackupAndRequest(t *testing.T) {
	h := newHarness(t, "v0.1.0", nil)
	h.src.rels = []Release{rel("v0.2.0", "")}
	_ = h.m.Check(context.Background())
	row, err := h.m.Apply(context.Background(), "v0.2.0", "a@percona.com")
	if err != nil {
		t.Fatal(err)
	}
	if row.Status != StatusApplying || row.RequestedBy != "a@percona.com" {
		t.Fatalf("row = %+v", row)
	}
	b, err := os.ReadFile(filepath.Join(h.applyDir, "apply-request"))
	if err != nil {
		t.Fatal(err)
	}
	var req map[string]any
	if err := json.Unmarshal(b, &req); err != nil {
		t.Fatal(err)
	}
	if len(req) != 5 || req["version"] != "v0.2.0" || req["previous"] != "v0.1.0" || req["requested_by"] != "a@percona.com" || req["requested_at"] != "2026-11-06T12:00:00Z" {
		t.Fatalf("request = %v", req)
	}
	name, _ := req["backup"].(string)
	if name != "pre-v0.2.0-20261106T120000Z.db" || !backupRe.MatchString(name) {
		t.Fatalf("backup name %q", name)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(h.dbPath), "backups", name)); err != nil {
		t.Fatalf("backup file: %v", err)
	}
	fi, _ := os.Stat(filepath.Join(h.applyDir, "apply-request"))
	if fi.Mode().Perm() != 0o644 {
		t.Fatalf("mode %v", fi.Mode().Perm())
	}
}

func TestApplyErrors(t *testing.T) {
	h := newHarness(t, "v0.2.0", nil)
	h.src.rels = []Release{rel("v0.1.0", ""), rel("v0.3.0", ""), rel("v0.4.0", "")}
	_ = h.m.Check(context.Background())
	ctx := context.Background()
	if _, err := h.m.Apply(ctx, "v9.9.9", "a"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown: %v", err)
	}
	if _, err := h.m.Apply(ctx, "v0.3.0", "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.m.Apply(ctx, "v0.4.0", "a"); !errors.Is(err, ErrConflict) {
		t.Errorf("in flight: %v", err)
	}
	h2 := newHarness(t, "v0.1.0", func(o *Options) { o.ApplyDir = "" })
	h2.src.rels = []Release{rel("v0.2.0", "")}
	_ = h2.m.Check(ctx)
	if _, err := h2.m.Apply(ctx, "v0.2.0", "a"); !errors.Is(err, ErrDisabled) {
		t.Errorf("disabled: %v", err)
	}
}

func TestApplyRetriesFailed(t *testing.T) {
	h := newHarness(t, "v0.1.0", nil)
	h.src.rels = []Release{rel("v0.2.0", "")}
	_ = h.m.Check(context.Background())
	_ = h.m.st.markApplying("v0.2.0", "a", fixedNow)
	_ = h.m.st.markFinished("v0.2.0", false, "boom", fixedNow)
	if _, err := h.m.Apply(context.Background(), "v0.2.0", "a"); err != nil {
		t.Fatalf("retry: %v", err)
	}
}

func TestApplyBackupFailureWritesNothing(t *testing.T) {
	h := newHarness(t, "v0.1.0", nil)
	h.src.rels = []Release{rel("v0.2.0", "")}
	_ = h.m.Check(context.Background())
	// A file where the backups directory should be makes MkdirAll fail.
	if err := os.WriteFile(filepath.Join(filepath.Dir(h.dbPath), "backups"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := h.m.Apply(context.Background(), "v0.2.0", "a"); err == nil {
		t.Fatal("want error")
	}
	if _, err := os.Stat(filepath.Join(h.applyDir, "apply-request")); !os.IsNotExist(err) {
		t.Fatal("request written despite failed backup")
	}
	if h.view(t).Releases[0].Status != StatusAvailable {
		t.Fatal("row changed")
	}
}

func TestBackupPruneKeepsFive(t *testing.T) {
	h := newHarness(t, "v0.1.0", nil)
	dir := filepath.Join(filepath.Dir(h.dbPath), "backups")
	_ = os.MkdirAll(dir, 0o750)
	for i := 0; i < 7; i++ {
		p := filepath.Join(dir, "pre-v0.0.1-2026010"+string(rune('1'+i))+"T000000Z.db")
		_ = os.WriteFile(p, []byte("x"), 0o640)
		old := time.Now().Add(-time.Duration(10-i) * time.Hour) // mtimes are wall-clock
		_ = os.Chtimes(p, old, old)
	}
	h.src.rels = []Release{rel("v0.2.0", "")}
	_ = h.m.Check(context.Background())
	if _, err := h.m.Apply(context.Background(), "v0.2.0", "a"); err != nil {
		t.Fatal(err)
	}
	entries, _ := filepath.Glob(filepath.Join(dir, "pre-*.db"))
	if len(entries) != 5 {
		t.Fatalf("kept %d backups", len(entries))
	}
	if _, err := os.Stat(filepath.Join(dir, "pre-v0.2.0-20261106T120000Z.db")); err != nil {
		t.Fatal("newest backup pruned")
	}
}

func TestHostRequirement(t *testing.T) {
	cases := []struct {
		name, current, hostFiles, target string
		wantReq                          string
		wantBlocked                      bool
		wantInstr                        int
	}{
		{"unknown host files", "v0.1.0", "", "v0.3.0", "v0.2.0", true, 1},
		{"host files behind", "v0.1.0", "v0.1.0", "v0.3.0", "v0.2.0", true, 1},
		{"host files equal", "v0.1.0", "v0.2.0", "v0.3.0", "v0.2.0", false, 1},
		{"host files ahead", "v0.1.0", "v0.5.0", "v0.3.0", "v0.2.0", false, 1},
		{"host change already behind current", "v0.2.0", "", "v0.3.0", "", false, 0},
		{"target before host change", "v0.1.0", "", "v0.1.5", "", false, 0},
		{"two host changes", "v0.1.0", "v0.2.0", "v0.4.0", "v0.4.0", true, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t, c.current, func(o *Options) { o.HostFilesVersion = c.hostFiles })
			h.src.rels = []Release{rel("v0.1.5", ""), rel("v0.2.0", "add FOO"), rel("v0.3.0", ""), rel("v0.4.0", "add BAR")}
			_ = h.m.Check(context.Background())
			var got *ReleaseView
			v := h.view(t)
			for i := range v.Releases {
				if v.Releases[i].Version == c.target {
					got = &v.Releases[i]
				}
			}
			if got == nil {
				t.Fatalf("target %s not in view", c.target)
			}
			if got.RequiresHost != c.wantReq || got.Blocked != c.wantBlocked || len(got.HostInstructions) != c.wantInstr || got.HostInstructions == nil {
				t.Fatalf("got req=%q blocked=%v instr=%v", got.RequiresHost, got.Blocked, got.HostInstructions)
			}
			if c.wantInstr == 2 && (got.HostInstructions[0].Version != "v0.2.0" || got.HostInstructions[1].Version != "v0.4.0") {
				t.Fatalf("order %v", got.HostInstructions)
			}
		})
	}
}

func TestApplyBlockedByHost(t *testing.T) {
	h := newHarness(t, "v0.1.0", nil)
	h.src.rels = []Release{rel("v0.2.0", "add FOO")}
	_ = h.m.Check(context.Background())
	_, err := h.m.Apply(context.Background(), "v0.2.0", "a")
	var hu *HostUpdateError
	if !errors.Is(err, ErrHostUpdateRequired) || !errors.As(err, &hu) || hu.RequiresHost != "v0.2.0" {
		t.Fatalf("err = %v", err)
	}
}

func writeStatusFile(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "apply-status"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func applyingHarness(t *testing.T) harness {
	h := newHarness(t, "v0.1.0", nil)
	h.src.rels = []Release{rel("v0.2.0", "")}
	_ = h.m.Check(context.Background())
	if _, err := h.m.Apply(context.Background(), "v0.2.0", "a"); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(filepath.Join(h.applyDir, "apply-request")) // host consumed it
	return h
}

func TestReconcileStatusOK(t *testing.T) {
	h := applyingHarness(t)
	writeStatusFile(t, h.applyDir, `{"version":"v0.2.0","previous":"v0.1.0","ok":true,"error":"","finished_at":"2026-11-06T12:03:10Z"}`)
	h.m.Reconcile(false)
	r := h.view(t).Releases[0]
	if r.Status != StatusApplied || r.FinishedAt == nil || r.FinishedAt.Format(time.RFC3339) != "2026-11-06T12:03:10Z" {
		t.Fatalf("row = %+v", r)
	}
	if _, err := os.Stat(filepath.Join(h.applyDir, "apply-status")); !os.IsNotExist(err) {
		t.Fatal("status file not consumed")
	}
}

func TestReconcileStatusFailed(t *testing.T) {
	h := applyingHarness(t)
	writeStatusFile(t, h.applyDir, `{"version":"v0.2.0","previous":"v0.1.0","ok":false,"error":"health check timed out; rolled back to v0.1.0","finished_at":"2026-11-06T12:03:10Z"}`)
	h.m.Reconcile(false)
	r := h.view(t).Releases[0]
	if r.Status != StatusFailed || !strings.Contains(r.Error, "rolled back") || h.view(t).InFlight != "" {
		t.Fatalf("row = %+v", r)
	}
}

func TestReconcileStatusWithMismatchedVersionReleasesLock(t *testing.T) {
	h := applyingHarness(t)
	writeStatusFile(t, h.applyDir, `{"version":"","previous":"v0.1.0","ok":false,"error":"invalid version in request: ''","finished_at":"2026-11-06T12:03:10Z"}`)
	h.m.Reconcile(false)
	if v := h.view(t); v.InFlight != "" || v.Releases[0].Status != StatusFailed {
		t.Fatalf("view = %+v", v)
	}
}

func TestReconcileMalformedStatusIsKept(t *testing.T) {
	h := applyingHarness(t)
	writeStatusFile(t, h.applyDir, `{not json`)
	h.m.Reconcile(false)
	if _, err := os.Stat(filepath.Join(h.applyDir, "apply-status")); err != nil {
		t.Fatal("malformed status removed")
	}
	if h.view(t).InFlight != "v0.2.0" {
		t.Fatal("row changed")
	}
}

func TestReconcileStartupInterrupted(t *testing.T) {
	h := applyingHarness(t)
	h.m.Reconcile(false)
	if h.view(t).InFlight != "v0.2.0" {
		t.Fatal("periodic reconcile must not mark interrupted")
	}
	h.m.Reconcile(true)
	r := h.view(t).Releases[0]
	if r.Status != StatusFailed || r.Error != "interrupted" {
		t.Fatalf("row = %+v", r)
	}
}

func TestReconcileStartupKeepsPendingRequest(t *testing.T) {
	h := newHarness(t, "v0.1.0", nil)
	h.src.rels = []Release{rel("v0.2.0", "")}
	_ = h.m.Check(context.Background())
	_, _ = h.m.Apply(context.Background(), "v0.2.0", "a") // request still on disk
	h.m.Reconcile(true)
	if h.view(t).InFlight != "v0.2.0" {
		t.Fatal("pending request marked interrupted")
	}
}

func TestValidateApplyDir(t *testing.T) {
	dir := t.TempDir()
	if err := ValidateApplyDir(dir); err != nil {
		t.Fatalf("dir: %v", err)
	}
	if err := ValidateApplyDir(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing: want error")
	}
	f := filepath.Join(dir, "file")
	_ = os.WriteFile(f, nil, 0o644)
	if err := ValidateApplyDir(f); err == nil {
		t.Fatal("file: want error")
	}
}
```

- [ ] **Step 2: Run the tests to confirm they fail**

Run: `cd backend && go test ./internal/upgrade/`
Expected: a build failure (`undefined: Manager`, and so on).

- [ ] **Step 3: Implement** `backend/internal/upgrade/files.go`:

```go
package upgrade

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const (
	requestFile = "apply-request"
	statusFile  = "apply-status"
)

// Request is the apply-request contract (spec §4). Fields may only be added.
type Request struct {
	Version     string `json:"version"`
	Previous    string `json:"previous"`
	Backup      string `json:"backup"`
	RequestedBy string `json:"requested_by"`
	RequestedAt string `json:"requested_at"`
}

// Status is the apply-status contract written by the host.
type Status struct {
	Version    string `json:"version"`
	Previous   string `json:"previous"`
	OK         bool   `json:"ok"`
	Error      string `json:"error"`
	FinishedAt string `json:"finished_at"`
}

// writeAtomic writes v as JSON to dir/name via temp file, fsync and rename.
func writeAtomic(dir, name string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "."+name+".*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	ok := false
	defer func() {
		if !ok {
			f.Close()
			os.Remove(tmp)
		}
	}()
	if _, err := f.Write(append(b, '\n')); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Chmod(0o644); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(dir, name)); err != nil {
		return err
	}
	ok = true
	return nil
}

// readStatus returns the host's apply-status. present is false when there is
// none; err is non-nil when the file exists but cannot be parsed.
func readStatus(dir string) (st Status, present bool, err error) {
	b, err := os.ReadFile(filepath.Join(dir, statusFile))
	if os.IsNotExist(err) {
		return Status{}, false, nil
	}
	if err != nil {
		return Status{}, true, err
	}
	if err := json.Unmarshal(b, &st); err != nil {
		return Status{}, true, fmt.Errorf("parse %s: %w", statusFile, err)
	}
	return st, true, nil
}

// ValidateApplyDir checks that dir exists, is a directory and is writable.
func ValidateApplyDir(dir string) error {
	fi, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("UPGRADE_APPLY_DIR %q: %w", dir, err)
	}
	if !fi.IsDir() {
		return fmt.Errorf("UPGRADE_APPLY_DIR %q is not a directory", dir)
	}
	f, err := os.CreateTemp(dir, ".probe.*")
	if err != nil {
		return fmt.Errorf("UPGRADE_APPLY_DIR %q is not writable: %w", dir, err)
	}
	name := f.Name()
	f.Close()
	return os.Remove(name)
}
```

`backend/internal/upgrade/manager.go`:
```go
package upgrade

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Errors returned by Apply.
var (
	ErrDisabled           = errors.New("applying upgrades is disabled")
	ErrConflict           = errors.New("upgrade conflict")
	ErrNotFound           = errors.New("unknown release")
	ErrHostUpdateRequired = errors.New("host update required")
)

// HostUpdateError reports that a release needs newer host files first.
type HostUpdateError struct{ RequiresHost string }

func (e *HostUpdateError) Error() string {
	return "host update required: host files must be at least " + e.RequiresHost
}

// Is makes errors.Is(err, ErrHostUpdateRequired) match.
func (e *HostUpdateError) Is(target error) bool { return target == ErrHostUpdateRequired }

const keepBackups = 5

// Options configures a Manager.
type Options struct {
	DB               *sql.DB
	DBPath           string // store.db_path; backups go to <dir>/backups
	Source           ReleaseSource
	Repo             string // owner/name, shown to the UI for host-file URLs
	Current          string // running version (version.Version)
	HostFilesVersion string // OBS_DASHBOARD_HOST_FILES_VERSION
	ApplyDir         string // UPGRADE_APPLY_DIR; "" disables applying
	Interval         time.Duration
	Now              func() time.Time
}

// HostInstruction is one release's "Host changes" text.
type HostInstruction struct {
	Version     string `json:"version"`
	HostChanges string `json:"host_changes"`
}

// ReleaseView is one release row as served to the UI.
type ReleaseView struct {
	Version          string            `json:"version"`
	Notes            string            `json:"notes"`
	URL              string            `json:"url"`
	PublishedAt      time.Time         `json:"published_at"`
	Status           string            `json:"status"`
	RequestedBy      string            `json:"requested_by"`
	RequestedAt      *time.Time        `json:"requested_at"`
	FinishedAt       *time.Time        `json:"finished_at"`
	Error            string            `json:"error"`
	HostChanges      string            `json:"host_changes"`
	RequiresHost     string            `json:"requires_host"`
	Blocked          bool              `json:"blocked"`
	HostInstructions []HostInstruction `json:"host_instructions"`
}

// View is the GET /api/upgrades body.
type View struct {
	Current          string        `json:"current"`
	HostFilesVersion string        `json:"host_files_version"`
	Repo             string        `json:"repo"`
	ApplyEnabled     bool          `json:"apply_enabled"`
	InFlight         string        `json:"in_flight"`
	CheckedAt        *time.Time    `json:"checked_at"`
	Releases         []ReleaseView `json:"releases"`
}

// Manager owns release checks and the apply handshake.
type Manager struct {
	o         Options
	st        store_
	mu        sync.Mutex
	checkedAt *time.Time
}

// NewManager returns a Manager; zero Interval means 1h, nil Now means time.Now.
func NewManager(o Options) *Manager {
	if o.Interval <= 0 {
		o.Interval = time.Hour
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Manager{o: o, st: store_{o.DB}}
}

// ApplyEnabled reports whether this deployment can apply upgrades.
func (m *Manager) ApplyEnabled() bool { return m.o.ApplyDir != "" && ValidVersion(m.o.Current) }

func (m *Manager) hostFiles() string {
	if ValidVersion(m.o.HostFilesVersion) {
		return m.o.HostFilesVersion
	}
	return ""
}

// Check fetches releases and records the ones newer than the running version
// (all of them on a dev build). The network call runs outside the lock.
func (m *Manager) Check(ctx context.Context) error {
	rels, err := m.o.Source.Releases(ctx)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	dev := !ValidVersion(m.o.Current)
	for _, r := range rels {
		if dev || Newer(r.Version, m.o.Current) {
			if err := m.st.upsert(r); err != nil {
				return err
			}
		}
	}
	if !dev {
		rows, err := m.st.all()
		if err != nil {
			return err
		}
		for _, r := range rows {
			if (r.Status == StatusAvailable || r.Status == StatusFailed) && !Newer(r.Version, m.o.Current) {
				if err := m.st.setStatus(r.Version, StatusSuperseded); err != nil {
					return err
				}
			}
		}
	}
	now := m.o.Now().UTC()
	m.checkedAt = &now
	return nil
}

// requirement returns the highest release in (current, target] with host
// changes, and every such release's instructions, oldest first.
func (m *Manager) requirement(rows []Row, target string) (string, []HostInstruction) {
	dev := !ValidVersion(m.o.Current)
	ins := []HostInstruction{}
	for i := len(rows) - 1; i >= 0; i-- { // rows are newest first
		r := rows[i]
		if r.HostChanges == "" || Newer(r.Version, target) {
			continue
		}
		if !dev && !Newer(r.Version, m.o.Current) {
			continue
		}
		ins = append(ins, HostInstruction{Version: r.Version, HostChanges: r.HostChanges})
	}
	if len(ins) == 0 {
		return "", ins
	}
	return ins[len(ins)-1].Version, ins
}

func (m *Manager) blocked(requires string) bool {
	if requires == "" {
		return false
	}
	hf := m.hostFiles()
	return hf == "" || Newer(requires, hf)
}

func (m *Manager) releaseView(rows []Row, r Row) ReleaseView {
	req, ins := m.requirement(rows, r.Version)
	return ReleaseView{
		Version: r.Version, Notes: r.Notes, URL: r.URL, PublishedAt: r.PublishedAt,
		Status: r.Status, RequestedBy: r.RequestedBy, RequestedAt: r.RequestedAt,
		FinishedAt: r.FinishedAt, Error: r.Error, HostChanges: r.HostChanges,
		RequiresHost: req, Blocked: m.blocked(req), HostInstructions: ins,
	}
}

// View returns the UI state: pending rows plus the last 10 finished ones.
func (m *Manager) View() (View, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rows, err := m.st.all()
	if err != nil {
		return View{}, err
	}
	v := View{
		Current: m.o.Current, HostFilesVersion: m.hostFiles(), Repo: m.o.Repo,
		ApplyEnabled: m.ApplyEnabled(), CheckedAt: m.checkedAt, Releases: []ReleaseView{},
	}
	history := 0
	for _, r := range rows {
		switch r.Status {
		case StatusAvailable, StatusApplying:
		case StatusApplied, StatusFailed:
			if history >= 10 {
				continue
			}
			history++
		default:
			continue
		}
		if r.Status == StatusApplying {
			v.InFlight = r.Version
		}
		v.Releases = append(v.Releases, m.releaseView(rows, r))
	}
	return v, nil
}

// Apply backs up the database, writes the apply request and marks the row
// applying. Nothing is written when any step before the request fails.
func (m *Manager) Apply(ctx context.Context, version, actor string) (ReleaseView, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.ApplyEnabled() {
		return ReleaseView{}, ErrDisabled
	}
	inFlight, err := m.st.applying()
	if err != nil {
		return ReleaseView{}, err
	}
	if inFlight != "" {
		return ReleaseView{}, fmt.Errorf("%w: %s is already being applied", ErrConflict, inFlight)
	}
	rows, err := m.st.all()
	if err != nil {
		return ReleaseView{}, err
	}
	idx := -1
	for i, r := range rows {
		if r.Version == version {
			idx = i
		}
	}
	if idx < 0 {
		return ReleaseView{}, ErrNotFound
	}
	row := rows[idx]
	if (row.Status != StatusAvailable && row.Status != StatusFailed) || !Newer(version, m.o.Current) {
		return ReleaseView{}, fmt.Errorf("%w: %s is not applicable", ErrConflict, version)
	}
	if req, _ := m.requirement(rows, version); m.blocked(req) {
		return ReleaseView{}, &HostUpdateError{RequiresHost: req}
	}
	backup, err := m.backup(version)
	if err != nil {
		return ReleaseView{}, err
	}
	now := m.o.Now().UTC()
	if err := writeAtomic(m.o.ApplyDir, requestFile, Request{
		Version: version, Previous: m.o.Current, Backup: backup,
		RequestedBy: actor, RequestedAt: now.Format(time.RFC3339),
	}); err != nil {
		return ReleaseView{}, fmt.Errorf("write %s: %w", requestFile, err)
	}
	if err := m.st.markApplying(version, actor, now); err != nil {
		return ReleaseView{}, err
	}
	slog.Info("upgrades: apply requested", "version", version, "previous", m.o.Current, "by", actor, "backup", backup)
	row.Status, row.RequestedBy, row.RequestedAt, row.FinishedAt, row.Error = StatusApplying, actor, &now, nil, ""
	rows[idx] = row
	return m.releaseView(rows, row), nil
}

// backup writes a consistent copy of the database next to it and keeps the
// newest keepBackups copies.
func (m *Manager) backup(target string) (string, error) {
	dir := filepath.Join(filepath.Dir(m.o.DBPath), "backups")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("backup dir: %w", err)
	}
	name := fmt.Sprintf("pre-%s-%s.db", target, m.o.Now().UTC().Format("20060102T150405Z"))
	path := filepath.Join(dir, name)
	if _, err := m.o.DB.Exec(`VACUUM INTO ?`, path); err != nil {
		os.Remove(path)
		return "", fmt.Errorf("backup: %w", err)
	}
	pruneBackups(dir, keepBackups)
	return name, nil
}

func pruneBackups(dir string, keep int) {
	paths, err := filepath.Glob(filepath.Join(dir, "pre-*.db"))
	if err != nil || len(paths) <= keep {
		return
	}
	type entry struct {
		path string
		mod  time.Time
	}
	entries := make([]entry, 0, len(paths))
	for _, p := range paths {
		if fi, err := os.Stat(p); err == nil {
			entries = append(entries, entry{p, fi.ModTime()})
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].mod.After(entries[j].mod) })
	for _, e := range entries[min(keep, len(entries)):] {
		if err := os.Remove(e.path); err != nil {
			slog.Warn("upgrades: prune backup", "path", e.path, "err", err)
		}
	}
}

// Reconcile consumes the host's apply-status. At startup it also fails an
// applying row whose request and status are both gone.
func (m *Manager) Reconcile(startup bool) {
	if m.o.ApplyDir == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	st, present, err := readStatus(m.o.ApplyDir)
	if err != nil {
		slog.Error("upgrades: cannot read apply-status; leaving it for an operator", "err", err)
		return
	}
	applying, err := m.st.applying()
	if err != nil {
		slog.Error("upgrades: reconcile", "err", err)
		return
	}
	now := m.o.Now().UTC()
	if present {
		target := applying
		if st.Version != "" && st.Version == applying {
			target = st.Version
		}
		if target == "" {
			target = st.Version
		}
		finished := now
		if t, err := time.Parse(time.RFC3339, st.FinishedAt); err == nil {
			finished = t
		}
		if target != "" {
			if err := m.st.markFinished(target, st.OK, st.Error, finished); err != nil {
				slog.Error("upgrades: reconcile", "err", err)
				return
			}
		}
		if err := os.Remove(filepath.Join(m.o.ApplyDir, statusFile)); err != nil {
			slog.Warn("upgrades: remove apply-status", "err", err)
		}
		slog.Info("upgrades: apply finished", "version", target, "ok", st.OK, "error", st.Error)
		return
	}
	if startup && applying != "" {
		if _, err := os.Stat(filepath.Join(m.o.ApplyDir, requestFile)); os.IsNotExist(err) {
			if err := m.st.markFinished(applying, false, "interrupted", now); err != nil {
				slog.Error("upgrades: reconcile", "err", err)
			}
			slog.Warn("upgrades: in-flight upgrade was interrupted", "version", applying)
		}
	}
}

// Run checks for releases now and every Interval, and reconciles the apply
// status every 15s while applying is enabled. Call Reconcile(true) before it.
func (m *Manager) Run(ctx context.Context) {
	m.checkLogged(ctx)
	check := time.NewTicker(m.o.Interval)
	defer check.Stop()
	var reconcile <-chan time.Time
	if m.o.ApplyDir != "" {
		t := time.NewTicker(15 * time.Second)
		defer t.Stop()
		reconcile = t.C
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-check.C:
			m.checkLogged(ctx)
		case <-reconcile:
			m.Reconcile(false)
		}
	}
}

func (m *Manager) checkLogged(ctx context.Context) {
	if err := m.Check(ctx); err != nil {
		slog.Warn("upgrades: release check failed", "err", err)
	}
}
```

Notes:
- `min` is a Go 1.21+ builtin, so it's fine on 1.25.
- If the linter flags the unused `ctx` parameter of `Apply`, keep it anyway. It is part of the API, so callers can cancel in the future.

- [ ] **Step 4: Run the tests**

Run: `cd backend && go test ./internal/upgrade/ -v 2>&1 | tail -40 && go vet ./internal/upgrade/`
Expected: all PASS; `vet` is clean.

- [ ] **Step 5: Commit**
```bash
git add backend/internal/upgrade
git commit -s -m "feat(upgrade): manager with backup, apply request and reconcile"
```

---

### Task 4: Config, API routes and wiring

**Goal:** Expose `/api/upgrades`, `/api/upgrades/check` and `/api/upgrades/{version}/apply` behind sign-in, and run the manager from `main`.

**Files:**
- Modify: `backend/internal/config/config.go` (`UpgradesConfig`, defaults, validation)
- Test: `backend/internal/config/upgrades_test.go`
- Create: `backend/internal/api/upgrades.go`
- Test: `backend/internal/api/upgrades_routes_test.go`
- Modify: `backend/internal/api/server.go` (add the `upg *upgrade.Manager` parameter and routes)
- Modify: `backend/internal/api/handlers_test.go`, `backend/internal/api/auth_routes_test.go` (pass `nil` for the new parameter)
- Modify: `backend/cmd/obsboard/main.go` (build the manager, validate the apply dir, `Reconcile(true)`, `go Run`)
- Modify: `config.yaml.example`, `backend/config.yaml.example` (an `upgrades:` block)

**Acceptance Criteria:**
- [ ] Config:
  - `upgrades.github_repo` defaults to `Percona-Lab/obs-dashboard`, and the env var `UPGRADES_GITHUB_REPO` overrides it. A value that isn't `owner/name` makes `Load` fail.
  - `upgrades.check_interval` defaults to `1h`, and the env var `UPGRADES_CHECK_INTERVAL` overrides it. Values under `5m` make `Load` fail.
  - `ApplyDir` comes from `UPGRADE_APPLY_DIR`, and `HostFilesVersion` from `OBS_DASHBOARD_HOST_FILES_VERSION`.
- [ ] `GET /api/upgrades` returns 401 without a session, and 200 with a session (the JSON `View`).
- [ ] `POST /api/upgrades/check` and `POST /api/upgrades/{version}/apply`:
  - return 403 with a bad or missing `Origin`;
  - apply returns 202, 404, 409 or 503 as in the spec;
  - when the release is blocked, apply returns 409 with `{"error":"host_update_required","requires_host":"vX.Y.Z"}`;
  - check returns 502 when GitHub fails.
- [ ] `main` behaviour:
  - fails startup when `UPGRADE_APPLY_DIR` is set but invalid;
  - calls `Reconcile(true)` before serving;
  - runs `Run` in a goroutine.

**Verify:** `cd backend && go test ./... ` → all ok

**Steps:**

- [ ] **Step 1: Write the failing config test** `backend/internal/config/upgrades_test.go`:

```go
package config

import (
	"testing"
	"time"
)

func TestUpgradesDefaults(t *testing.T) {
	t.Setenv("CONFIG_FILE", "/nonexistent.yaml")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Upgrades.GitHubRepo != "Percona-Lab/obs-dashboard" || cfg.Upgrades.CheckInterval != time.Hour || cfg.Upgrades.ApplyDir != "" {
		t.Fatalf("upgrades = %+v", cfg.Upgrades)
	}
}

func TestUpgradesEnv(t *testing.T) {
	t.Setenv("CONFIG_FILE", "/nonexistent.yaml")
	t.Setenv("UPGRADES_GITHUB_REPO", "acme/dash")
	t.Setenv("UPGRADES_CHECK_INTERVAL", "30m")
	t.Setenv("UPGRADE_APPLY_DIR", "/apply")
	t.Setenv("OBS_DASHBOARD_HOST_FILES_VERSION", "v0.3.0")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	u := cfg.Upgrades
	if u.GitHubRepo != "acme/dash" || u.CheckInterval != 30*time.Minute || u.ApplyDir != "/apply" || u.HostFilesVersion != "v0.3.0" {
		t.Fatalf("upgrades = %+v", u)
	}
}

func TestUpgradesValidation(t *testing.T) {
	for name, env := range map[string][2]string{
		"short interval": {"UPGRADES_CHECK_INTERVAL", "1m"},
		"bad interval":   {"UPGRADES_CHECK_INTERVAL", "soon"},
		"bad repo":       {"UPGRADES_GITHUB_REPO", "not a repo"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("CONFIG_FILE", "/nonexistent.yaml")
			t.Setenv(env[0], env[1])
			if _, err := Load(); err == nil {
				t.Fatal("want error")
			}
		})
	}
}
```

The existing config tests set `OBS_USERNAME` before calling `Load()`. Add `t.Setenv("OBS_USERNAME", "u")` right after each `t.Setenv("CONFIG_FILE", …)` above.

- [ ] **Step 2: Implement the config.** In `backend/internal/config/config.go`:
- Add `Upgrades UpgradesConfig` to `Config`, after `Auth`.
- Add the type and loader:
```go
// UpgradesConfig configures release checks and UI-applied upgrades.
type UpgradesConfig struct {
	GitHubRepo       string
	CheckInterval    time.Duration
	ApplyDir         string // UPGRADE_APPLY_DIR; empty disables applying
	HostFilesVersion string // OBS_DASHBOARD_HOST_FILES_VERSION
}

var repoRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

func loadUpgrades(v *viper.Viper) (UpgradesConfig, error) {
	interval, err := time.ParseDuration(v.GetString("upgrades.check_interval"))
	if err != nil {
		return UpgradesConfig{}, fmt.Errorf("invalid UPGRADES_CHECK_INTERVAL %q: %w", v.GetString("upgrades.check_interval"), err)
	}
	if interval < 5*time.Minute {
		return UpgradesConfig{}, fmt.Errorf("upgrades.check_interval must be at least 5m, got %s", interval)
	}
	repo := strings.TrimSpace(v.GetString("upgrades.github_repo"))
	if !repoRe.MatchString(repo) {
		return UpgradesConfig{}, fmt.Errorf("upgrades.github_repo must be owner/name, got %q", repo)
	}
	return UpgradesConfig{
		GitHubRepo:       repo,
		CheckInterval:    interval,
		ApplyDir:         strings.TrimSpace(os.Getenv("UPGRADE_APPLY_DIR")),
		HostFilesVersion: strings.TrimSpace(os.Getenv("OBS_DASHBOARD_HOST_FILES_VERSION")),
	}, nil
}
```
- Add `"regexp"` to the imports.
- In `Load()`, add the defaults `v.SetDefault("upgrades.github_repo", "Percona-Lab/obs-dashboard")` and `v.SetDefault("upgrades.check_interval", "1h")`. Add `{"upgrades.github_repo", "UPGRADES_GITHUB_REPO"}` and `{"upgrades.check_interval", "UPGRADES_CHECK_INTERVAL"}` to the `BindEnv` list. After `cfg.Auth = authCfg`, add:
```go
	upgradesCfg, err := loadUpgrades(v)
	if err != nil {
		return nil, err
	}
	cfg.Upgrades = upgradesCfg
```
- In both `config.yaml.example` files, append:
```yaml
# Release checks for upgrades from Settings → Service. Applying requires the
# host files described in deploy/host/README.md (UPGRADE_APPLY_DIR).
upgrades:
  github_repo: Percona-Lab/obs-dashboard
  check_interval: 1h
```

Run: `cd backend && go test ./internal/config/ -v 2>&1 | tail -5` → PASS.

- [ ] **Step 3: Write the failing API test** `backend/internal/api/upgrades_routes_test.go`:

```go
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/percona/obs-dashboard/internal/auth"
	"github.com/percona/obs-dashboard/internal/hub"
	"github.com/percona/obs-dashboard/internal/obs"
	"github.com/percona/obs-dashboard/internal/store"
	"github.com/percona/obs-dashboard/internal/upgrade"
	"golang.org/x/oauth2"
)

type staticSource []upgrade.Release

func (s staticSource) Releases(context.Context) ([]upgrade.Release, error) { return s, nil }

func upgradeRouter(t *testing.T, applyDir, hostFiles string) (http.Handler, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "obsboard.db")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	opts := auth.Options{PublicURL: dashOrigin, Admins: []string{"alice@percona.com"}, Provider: auth.NewProvider(oauth2.Config{}, nil)}
	sid, err := auth.NewStore(db).CreateSession("alice@percona.com", time.Now(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	pub := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	m := upgrade.NewManager(upgrade.Options{
		DB: db, DBPath: dbPath, Repo: "Percona-Lab/obs-dashboard", Current: "v0.1.0",
		HostFilesVersion: hostFiles, ApplyDir: applyDir,
		Source: staticSource{
			{Version: "v0.2.0", Notes: "n2", URL: "u2", PublishedAt: pub},
			{Version: "v0.3.0", Notes: "n3", URL: "u3", PublishedAt: pub, HostChanges: "add FOO"},
		},
	})
	if err := m.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	obsClient := obs.SingleFleet(obs.NewClient(stubOBSServer(t).URL, "user", "pass"), "isv:percona")
	r := NewRouter(db, hub.New(), obsClient, fakeStatter{}, new(atomic.Bool), time.Duration(0), fakePollState{s: "active"}, auth.NewService(db, opts), m)
	return r, sid
}

func callRec(h http.Handler, method, path, origin, sid string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if sid != "" {
		req.AddCookie(&http.Cookie{Name: "obsboard_session", Value: sid})
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestUpgradesRequireSignIn(t *testing.T) {
	h, sid := upgradeRouter(t, t.TempDir(), "v0.3.0")
	if rec := callRec(h, http.MethodGet, "/api/upgrades", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anon GET: %d", rec.Code)
	}
	if rec := callRec(h, http.MethodPost, "/api/upgrades/v0.2.0/apply", "http://evil.test", sid); rec.Code != http.StatusForbidden {
		t.Fatalf("bad origin: %d", rec.Code)
	}
	if rec := callRec(h, http.MethodPost, "/api/upgrades/check", "", sid); rec.Code != http.StatusForbidden {
		t.Fatalf("missing origin: %d", rec.Code)
	}
}

func TestUpgradesListAndApply(t *testing.T) {
	applyDir := t.TempDir()
	h, sid := upgradeRouter(t, applyDir, "v0.3.0")
	rec := callRec(h, http.MethodGet, "/api/upgrades", "", sid)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET: %d", rec.Code)
	}
	var v upgrade.View
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if v.Current != "v0.1.0" || len(v.Releases) != 2 || !v.ApplyEnabled || v.Repo != "Percona-Lab/obs-dashboard" {
		t.Fatalf("view = %+v", v)
	}
	if rec := callRec(h, http.MethodPost, "/api/upgrades/v0.2.0/apply", dashOrigin, sid); rec.Code != http.StatusAccepted {
		t.Fatalf("apply: %d %s", rec.Code, rec.Body)
	}
	if _, err := os.Stat(filepath.Join(applyDir, "apply-request")); err != nil {
		t.Fatal("no request written")
	}
	if rec := callRec(h, http.MethodPost, "/api/upgrades/v0.3.0/apply", dashOrigin, sid); rec.Code != http.StatusConflict {
		t.Fatalf("in flight: %d", rec.Code)
	}
	if rec := callRec(h, http.MethodPost, "/api/upgrades/v9.9.9/apply", dashOrigin, sid); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown: %d", rec.Code)
	}
}

func TestUpgradesHostUpdateRequired(t *testing.T) {
	h, sid := upgradeRouter(t, t.TempDir(), "")
	rec := callRec(h, http.MethodPost, "/api/upgrades/v0.3.0/apply", dashOrigin, sid)
	if rec.Code != http.StatusConflict {
		t.Fatalf("code %d", rec.Code)
	}
	var body map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["error"] != "host_update_required" || body["requires_host"] != "v0.3.0" {
		t.Fatalf("body %v", body)
	}
}

func TestUpgradesDisabled(t *testing.T) {
	h, sid := upgradeRouter(t, "", "v0.3.0")
	if rec := callRec(h, http.MethodPost, "/api/upgrades/v0.2.0/apply", dashOrigin, sid); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("code %d", rec.Code)
	}
}

func TestUpgradesCheck(t *testing.T) {
	h, sid := upgradeRouter(t, t.TempDir(), "v0.3.0")
	if rec := callRec(h, http.MethodPost, "/api/upgrades/check", dashOrigin, sid); rec.Code != http.StatusOK {
		t.Fatalf("check: %d", rec.Code)
	}
}
```

- [ ] **Step 4: Implement** `backend/internal/api/upgrades.go`:

```go
package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/percona/obs-dashboard/internal/auth"
	"github.com/percona/obs-dashboard/internal/upgrade"
)

func respondJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func upgradesListHandler(m *upgrade.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		v, err := m.View()
		if err != nil {
			slog.Error("upgrades: view", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		respondJSON(w, http.StatusOK, v)
	}
}

func upgradesCheckHandler(m *upgrade.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := m.Check(r.Context()); err != nil {
			slog.Warn("upgrades: manual check failed", "err", err)
			http.Error(w, "release check failed: "+err.Error(), http.StatusBadGateway)
			return
		}
		upgradesListHandler(m)(w, r)
	}
}

func upgradesApplyHandler(m *upgrade.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, _ := auth.UserFrom(r.Context())
		row, err := m.Apply(r.Context(), chi.URLParam(r, "version"), actor)
		var hu *upgrade.HostUpdateError
		switch {
		case err == nil:
			respondJSON(w, http.StatusAccepted, row)
		case errors.As(err, &hu):
			respondJSON(w, http.StatusConflict, map[string]string{"error": "host_update_required", "requires_host": hu.RequiresHost})
		case errors.Is(err, upgrade.ErrNotFound):
			http.Error(w, err.Error(), http.StatusNotFound)
		case errors.Is(err, upgrade.ErrConflict):
			http.Error(w, err.Error(), http.StatusConflict)
		case errors.Is(err, upgrade.ErrDisabled):
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
		default:
			slog.Error("upgrades: apply", "err", err)
			http.Error(w, "upgrade request failed: "+err.Error(), http.StatusInternalServerError)
		}
	}
}
```

In `backend/internal/api/server.go`:
- Add the import `"github.com/percona/obs-dashboard/internal/upgrade"`.
- Append the parameter `upg *upgrade.Manager` to `NewRouter`, after `authSvc *auth.Service`.
- Before `return r`, add:
```go
	// Upgrades are an admin matter: even reads need a signed-in user.
	if upg != nil {
		r.Group(func(r chi.Router) {
			r.Use(authSvc.RequireUser)
			r.Get("/api/upgrades", upgradesListHandler(upg))
			r.With(authSvc.CheckOrigin).Post("/api/upgrades/check", upgradesCheckHandler(upg))
			r.With(authSvc.CheckOrigin).Post("/api/upgrades/{version}/apply", upgradesApplyHandler(upg))
		})
	}
```
Update the 4 existing `NewRouter(` calls in `handlers_test.go` (3) and `auth_routes_test.go` (1) to pass `, nil` as the final argument.

In `backend/cmd/obsboard/main.go`, after the `authSvc.PruneExpired()` block:
```go
	if cfg.Upgrades.ApplyDir != "" {
		if err := upgrade.ValidateApplyDir(cfg.Upgrades.ApplyDir); err != nil {
			return fmt.Errorf("upgrades: %w", err)
		}
	}
	upg := upgrade.NewManager(upgrade.Options{
		DB:               db,
		DBPath:           cfg.Store.DBPath,
		Source:           upgrade.GitHubSource{Repo: cfg.Upgrades.GitHubRepo},
		Repo:             cfg.Upgrades.GitHubRepo,
		Current:          version.Version,
		HostFilesVersion: cfg.Upgrades.HostFilesVersion,
		ApplyDir:         cfg.Upgrades.ApplyDir,
		Interval:         cfg.Upgrades.CheckInterval,
	})
	upg.Reconcile(true)
	go upg.Run(ctx)
	slog.Info("upgrades: release checks enabled", "repo", cfg.Upgrades.GitHubRepo, "apply_enabled", upg.ApplyEnabled(), "host_files", cfg.Upgrades.HostFilesVersion)
```
Then pass `upg` as the last argument of `api.NewRouter(…)`, and add the `upgrade` import.

- [ ] **Step 5: Run everything and commit**

Run: `cd backend && go vet ./... && go test ./...`
Expected: all packages ok.
```bash
git add backend config.yaml.example
git commit -s -m "feat(api): upgrade routes, config and wiring"
```

---

### Task 5: Frontend upgrades state and Settings "Service" zone

**Goal:** Settings shows the running version, newer releases and their notes, a confirmed Upgrade action with live progress through the restart, the host-update steps for blocked releases, and the upgrade history.

**Files:**
- Create: `frontend/src/composables/http.ts`
- Modify: `frontend/src/composables/useSettingsUsers.ts` (import `errorText` from `./http`, delete the local copy)
- Create: `frontend/src/composables/useUpgrades.ts`
- Create: `frontend/src/components/HostUpdateSteps.vue`
- Create: `frontend/src/components/ServiceZone.vue`
- Modify: `frontend/src/components/SettingsPanel.vue` (render `<ServiceZone />` above the Access section)
- Modify: `frontend/src/components/SettingsPanel.test.ts` (route `/api/upgrades` in the default fetch handler)
- Test: `frontend/src/components/ServiceZone.test.ts`, `frontend/src/composables/useUpgrades.test.ts`

**Acceptance Criteria:**
- [ ] The zone shows `current`, the last-check time, a "Check now" button (`POST /api/upgrades/check`), and the release rows (`available`/`applying`) with their notes as pre-wrapped text.
- [ ] Upgrade flow:
  1. Clicking "Upgrade" shows confirm and cancel buttons.
  2. Confirm sends `POST /api/upgrades/{v}/apply`.
  3. The page then polls every 5s.
  4. Network errors and 502/503/504 responses, including HTML bodies, show "Restarting…".
  5. When `current` equals the target, the zone shows "Upgraded to vX" and a Reload button.
  6. A `failed` target row shows the error.
- [ ] The Upgrade button is disabled when `apply_enabled` is false (with an explanatory note), when an upgrade is in flight, or when the row is `blocked`. A blocked row's button reads "Host update required" and shows `HostUpdateSteps`.
- [ ] `HostUpdateSteps` shows each host instruction labelled with its version. It shows the manual commands built for `requires_host` and `repo`, with no deployment-tool names, and the installed host-files version or "unknown — set OBS_DASHBOARD_HOST_FILES_VERSION".
- [ ] The existing `SettingsPanel` tests still pass.

**Verify:** `cd frontend && npx vitest run src/components/ServiceZone.test.ts src/composables/useUpgrades.test.ts src/components/SettingsPanel.test.ts` → PASS

**Steps:**

- [ ] **Step 1: Write the failing tests**

`frontend/src/composables/useUpgrades.test.ts`:
```ts
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { hostUpdateCommands, refreshUpgrades, useUpgrades, resetUpgradesForTests } from './useUpgrades'
import { resetAuthForTests } from './useAuth'

beforeEach(() => { resetAuthForTests(); resetUpgradesForTests() })
afterEach(() => vi.unstubAllGlobals())

describe('hostUpdateCommands', () => {
  it('builds tool-agnostic manual steps for the release', () => {
    const c = hostUpdateCommands('Percona-Lab/obs-dashboard', 'v0.3.0')
    expect(c).toContain('https://github.com/Percona-Lab/obs-dashboard/releases/download/v0.3.0/obs-dashboard-host-v0.3.0.tar.gz')
    expect(c).toContain('sha256sum -c --ignore-missing SHA256SUMS')
    expect(c).toContain('OBS_DASHBOARD_HOST_FILES_VERSION=v0.3.0')
    expect(c).toContain('systemctl restart obs-dashboard')
    expect(c.startsWith('cd "$(mktemp -d)"')).toBe(true)
    expect(c.endsWith('systemctl restart obs-dashboard')).toBe(true)
  })
})

describe('refreshUpgrades', () => {
  it('treats an HTML 502 from the proxy as unreachable', async () => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response('<html>Bad Gateway</html>', { status: 502 }))))
    expect(await refreshUpgrades()).toBe('unreachable')
  })
  it('treats a network failure as unreachable', async () => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.reject(new TypeError('failed'))))
    expect(await refreshUpgrades()).toBe('unreachable')
  })
  it('rejects a body that is not an upgrades view', async () => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response('{"admins":[]}', { status: 200 }))))
    expect(await refreshUpgrades()).toBe('error')
    expect(useUpgrades().view.value).toBeNull()
  })
})
```

`frontend/src/components/ServiceZone.test.ts`:
```ts
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import ServiceZone from './ServiceZone.vue'
import { resetAuthForTests, useAuth } from '../composables/useAuth'
import { resetUpgradesForTests, type UpgradesView, type ReleaseView } from '../composables/useUpgrades'

function release(over: Partial<ReleaseView>): ReleaseView {
  return {
    version: 'v0.2.0', notes: 'Fixed things', url: 'https://h/v0.2.0', published_at: '2026-11-02T10:00:00Z',
    status: 'available', requested_by: '', requested_at: null, finished_at: null, error: '',
    host_changes: '', requires_host: '', blocked: false, host_instructions: [], ...over,
  }
}

function view(over: Partial<UpgradesView> = {}): UpgradesView {
  return {
    current: 'v0.1.0', host_files_version: 'v0.1.0', repo: 'Percona-Lab/obs-dashboard', apply_enabled: true,
    in_flight: '', checked_at: '2026-11-06T12:00:00Z',
    releases: [release({})], ...over,
  }
}

type Handler = (url: string, init?: RequestInit) => Promise<Response> | Response
let handler: Handler
let fetchFn: ReturnType<typeof vi.fn>
const json = (b: unknown, status = 200) => new Response(JSON.stringify(b), { status, headers: { 'Content-Type': 'application/json' } })

beforeEach(() => {
  resetAuthForTests()
  resetUpgradesForTests()
  useAuth().user.value = 'alice@percona.com'
  handler = () => json(view())
  fetchFn = vi.fn((url: string, init?: RequestInit) => Promise.resolve(handler(url, init)))
  vi.stubGlobal('fetch', fetchFn)
})
afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals() })

async function mounted() {
  const w = mount(ServiceZone)
  await flushPromises()
  return w
}

describe('ServiceZone', () => {
  it('shows the current version, releases with notes, and history', async () => {
    handler = () => json(view({ releases: [release({}), release({ version: 'v0.0.9', status: 'failed', error: 'rolled back', finished_at: '2026-11-01T10:00:00Z' })] }))
    const w = await mounted()
    expect(w.find('[data-testid="current-version"]').text()).toBe('v0.1.0')
    expect(w.findAll('[data-testid="release-row"]')).toHaveLength(1)
    expect(w.find('[data-testid="notes"]').text()).toBe('Fixed things')
    const hist = w.findAll('[data-testid="history-row"]')
    expect(hist).toHaveLength(1)
    expect(hist[0].text()).toContain('rolled back')
  })

  it('confirms, applies, shows Restarting during the restart, then offers Reload', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'setInterval', 'clearInterval'] })
    const w = await mounted()
    await w.find('[data-testid="upgrade"]').trigger('click')
    expect(w.find('[data-testid="confirm"]').exists()).toBe(true)
    handler = (url, init) => {
      if (url.endsWith('/apply') && init?.method === 'POST') return json(release({ status: 'applying' }), 202)
      return json(view({ in_flight: 'v0.2.0', releases: [release({ status: 'applying' })] }))
    }
    await w.find('[data-testid="confirm"]').trigger('click')
    await flushPromises()
    expect(fetchFn.mock.calls.some(([u, i]) => u === '/api/upgrades/v0.2.0/apply' && i?.method === 'POST')).toBe(true)
    expect(w.find('[data-testid="progress"]').text()).toContain('Upgrading to v0.2.0')

    handler = () => new Response('<html>502</html>', { status: 502 })
    await vi.advanceTimersByTimeAsync(5000)
    await flushPromises()
    expect(w.find('[data-testid="progress"]').text()).toContain('Restarting')

    handler = () => json(view({ current: 'v0.2.0', releases: [release({ status: 'applied', finished_at: '2026-11-06T12:03:00Z' })] }))
    await vi.advanceTimersByTimeAsync(5000)
    await flushPromises()
    expect(w.find('[data-testid="progress"]').text()).toContain('Upgraded to v0.2.0')
    expect(w.find('[data-testid="reload"]').exists()).toBe(true)
  })

  it('shows the failure when the upgrade is rolled back', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'setInterval', 'clearInterval'] })
    handler = () => json(view({ in_flight: 'v0.2.0', releases: [release({ status: 'applying' })] }))
    const w = await mounted()
    handler = () => json(view({ releases: [release({ status: 'failed', error: 'health check timed out; rolled back to v0.1.0' })] }))
    await vi.advanceTimersByTimeAsync(5000)
    await flushPromises()
    expect(w.find('[data-testid="failure"]').text()).toContain('rolled back to v0.1.0')
  })

  it('disables Upgrade when applying is not enabled', async () => {
    handler = () => json(view({ apply_enabled: false }))
    const w = await mounted()
    expect(w.find('[data-testid="apply-disabled"]').exists()).toBe(true)
    expect((w.find('[data-testid="upgrade"]').element as HTMLButtonElement).disabled).toBe(true)
  })

  it('shows host update steps on a blocked release', async () => {
    handler = () => json(view({
      host_files_version: '',
      releases: [release({ version: 'v0.3.0', blocked: true, requires_host: 'v0.3.0', host_changes: 'Add FOO=1 to /etc/obs-dashboard/env', host_instructions: [{ version: 'v0.3.0', host_changes: 'Add FOO=1 to /etc/obs-dashboard/env' }] })],
    }))
    const w = await mounted()
    const btn = w.find('[data-testid="upgrade"]')
    expect(btn.text()).toBe('Host update required')
    expect((btn.element as HTMLButtonElement).disabled).toBe(true)
    expect(w.find('[data-testid="host-instruction"]').text()).toContain('Add FOO=1')
    expect(w.find('[data-testid="host-commands"]').text()).toContain('obs-dashboard-host-v0.3.0.tar.gz')
    expect(w.find('[data-testid="host-files-version"]').text()).toContain('unknown')
  })

  it('reports a failed check', async () => {
    const w = await mounted()
    handler = (url, init) => (init?.method === 'POST' ? new Response('release check failed: HTTP 403', { status: 502 }) : json(view()))
    await w.find('[data-testid="check-now"]').trigger('click')
    await flushPromises()
    expect(w.find('[data-testid="action-error"]').text()).toContain('release check failed')
  })
})
```

In `frontend/src/components/SettingsPanel.test.ts`, change the default handler in `beforeEach` to:
```ts
  handler = (url: string) => (url.startsWith('/api/upgrades')
    ? new Response(JSON.stringify({ current: 'v0.1.0', host_files_version: '', repo: 'Percona-Lab/obs-dashboard', apply_enabled: false, in_flight: '', checked_at: null, releases: [] }), { status: 200 })
    : new Response(JSON.stringify(listBody), { status: 200 }))
```
Also add `import { resetUpgradesForTests } from '../composables/useUpgrades'` and call it in `beforeEach`. Any test there that overrides `handler` must still return the users list body for `/api/settings/users`. Check each override and keep the `/api/upgrades` branch where a test replaces the handler.

- [ ] **Step 2: Run the tests to confirm they fail**

Run: `cd frontend && npx vitest run src/components/ServiceZone.test.ts src/composables/useUpgrades.test.ts`
Expected: FAIL (the modules can't be resolved).

Fake timers fake only `setTimeout`/`setInterval`. Vue Test Utils' `flushPromises` schedules with `setImmediate`, and would hang if that were faked as well.

- [ ] **Step 3: Implement**

`frontend/src/composables/http.ts`:
```ts
// errorText returns a failed response's text body, or "HTTP <status>".
export async function errorText(res: Response): Promise<string> {
  const text = (await res.text().catch(() => '')).trim()
  return text || `HTTP ${res.status}`
}
```
In `useSettingsUsers.ts`, delete the local `errorText` function and add `import { errorText } from './http'`.

`frontend/src/composables/useUpgrades.ts`:
```ts
import { computed, ref } from 'vue'
import { handleUnauthorized } from './useAuth'
import { errorText } from './http'

export type ReleaseStatus = 'available' | 'applying' | 'applied' | 'failed' | 'superseded'

export interface HostInstruction {
  version: string
  host_changes: string
}

export interface ReleaseView {
  version: string
  notes: string
  url: string
  published_at: string
  status: ReleaseStatus
  requested_by: string
  requested_at: string | null
  finished_at: string | null
  error: string
  host_changes: string
  requires_host: string
  blocked: boolean
  host_instructions: HostInstruction[]
}

export interface UpgradesView {
  current: string
  host_files_version: string
  repo: string
  apply_enabled: boolean
  in_flight: string
  checked_at: string | null
  releases: ReleaseView[]
}

export const NOTICE_REFRESH_MS = 15 * 60 * 1000

// Module-level singleton shared by the Service zone, header dot and banner.
const view = ref<UpgradesView | null>(null)
const loadError = ref<string | null>(null)
let noticeTimer: ReturnType<typeof setInterval> | undefined

const updateAvailable = computed(() => view.value?.releases.some((r) => r.status === 'available') ?? false)
// The newest available release, when it needs a host update first.
const blockedRelease = computed<ReleaseView | null>(() => {
  const newest = view.value?.releases.find((r) => r.status === 'available')
  return newest && newest.blocked ? newest : null
})

function isView(b: unknown): b is UpgradesView {
  if (typeof b !== 'object' || b === null) return false
  const v = b as Partial<UpgradesView>
  return typeof v.current === 'string' && Array.isArray(v.releases)
}

export type RefreshResult = 'ok' | 'unreachable' | 'unauthorized' | 'error'

// refreshUpgrades reloads the view. 'unreachable' covers the restart window:
// network errors, proxy 502/503/504 pages and non-JSON bodies.
export async function refreshUpgrades(): Promise<RefreshResult> {
  let res: Response
  try {
    res = await fetch('/api/upgrades')
  } catch {
    return 'unreachable'
  }
  if (res.status === 401) { handleUnauthorized(); return 'unauthorized' }
  if (res.status === 502 || res.status === 503 || res.status === 504) return 'unreachable'
  if (!res.ok) { loadError.value = await errorText(res); return 'error' }
  let body: unknown
  try {
    body = await res.json()
  } catch {
    return 'unreachable'
  }
  if (!isView(body)) { loadError.value = 'Unexpected response from /api/upgrades'; return 'error' }
  view.value = body
  loadError.value = null
  return 'ok'
}

// checkNow asks the server to check GitHub now; returns an error or null.
export async function checkNow(): Promise<string | null> {
  try {
    const res = await fetch('/api/upgrades/check', { method: 'POST' })
    if (res.status === 401) { handleUnauthorized(); return 'Session expired' }
    if (!res.ok) return await errorText(res)
    const body: unknown = await res.json()
    if (isView(body)) view.value = body
    return null
  } catch {
    return 'Network error'
  }
}

// applyUpgrade requests an upgrade; returns an error message or null.
export async function applyUpgrade(version: string): Promise<string | null> {
  try {
    const res = await fetch(`/api/upgrades/${encodeURIComponent(version)}/apply`, { method: 'POST' })
    if (res.status === 401) { handleUnauthorized(); return 'Session expired' }
    if (res.status === 409 && (res.headers.get('Content-Type') ?? '').includes('application/json')) {
      const body = (await res.json()) as { error?: string; requires_host?: string }
      if (body.error === 'host_update_required') return `Host update required: install host files ${body.requires_host} first`
    }
    if (!res.ok) return await errorText(res)
    await refreshUpgrades()
    return null
  } catch {
    return 'Network error'
  }
}

// hostUpdateCommands is the manual, tool-agnostic host-files update for v.
export function hostUpdateCommands(repo: string, v: string): string {
  const base = `https://github.com/${repo}/releases/download/${v}`
  const tar = `obs-dashboard-host-${v}.tar.gz`
  return [
    'cd "$(mktemp -d)"',
    `curl -fLO ${base}/${tar}`,
    `curl -fLO ${base}/SHA256SUMS`,
    'sha256sum -c --ignore-missing SHA256SUMS',
    `tar xzf ${tar}`,
    'install -m 0644 obs-dashboard.container /etc/containers/systemd/',
    'install -D -m 0755 apply.sh /usr/local/lib/obs-dashboard/apply.sh',
    'install -m 0644 obs-dashboard-apply.path obs-dashboard-apply.service /etc/systemd/system/',
    '# apply the release-specific host changes listed above, then:',
    "grep -q '^OBS_DASHBOARD_HOST_FILES_VERSION=' /etc/obs-dashboard/env \\",
    `  && sed -i 's/^OBS_DASHBOARD_HOST_FILES_VERSION=.*/OBS_DASHBOARD_HOST_FILES_VERSION=${v}/' /etc/obs-dashboard/env \\`,
    `  || echo 'OBS_DASHBOARD_HOST_FILES_VERSION=${v}' >> /etc/obs-dashboard/env`,
    'systemctl daemon-reload',
    'systemctl enable --now obs-dashboard-apply.path',
    'systemctl restart obs-dashboard',
  ].join('\n')
}

// startUpgradeNotices loads the view now and every 15 minutes (signed in).
export function startUpgradeNotices(): void {
  stopUpgradeNotices()
  void refreshUpgrades()
  noticeTimer = setInterval(() => { void refreshUpgrades() }, NOTICE_REFRESH_MS)
}

// stopUpgradeNotices stops refreshing and forgets the view (signed out).
export function stopUpgradeNotices(): void {
  if (noticeTimer !== undefined) clearInterval(noticeTimer)
  noticeTimer = undefined
  view.value = null
}

export function resetUpgradesForTests(): void {
  stopUpgradeNotices()
  loadError.value = null
}

export function useUpgrades() {
  return { view, loadError, updateAvailable, blockedRelease }
}
```

`frontend/src/components/HostUpdateSteps.vue`:
```vue
<script setup lang="ts">
import { computed } from 'vue'
import { hostUpdateCommands, type ReleaseView } from '../composables/useUpgrades'

const props = defineProps<{ release: ReleaseView; repo: string; hostFilesVersion: string }>()
const commands = computed(() => hostUpdateCommands(props.repo, props.release.requires_host))
const guide = computed(() => `https://github.com/${props.repo}/blob/${props.release.requires_host}/deploy/host/README.md`)
</script>

<template>
  <div data-testid="host-update-steps" class="flex flex-col gap-2 text-[12.5px]">
    <div v-for="i in release.host_instructions" :key="i.version" data-testid="host-instruction" class="flex flex-col gap-[2px]">
      <span class="font-semibold text-text-primary">Host changes in {{ i.version }}</span>
      <span class="whitespace-pre-wrap text-text-secondary">{{ i.host_changes }}</span>
    </div>
    <span class="text-text-secondary">Run as root on the host:</span>
    <pre data-testid="host-commands" class="m-0 overflow-x-auto rounded-[8px] border border-border bg-bg-app p-3 font-mono text-[12px] text-text-primary">{{ commands }}</pre>
    <span class="text-text-muted">
      If your host is managed by a deployment tool, make the equivalent change there instead, so it is not reverted on the
      next run. The dashboard restarts with the new host files, and this notice clears.
    </span>
    <span class="text-text-muted">
      Installed host files:
      <span data-testid="host-files-version" class="font-mono">{{ hostFilesVersion || 'unknown — set OBS_DASHBOARD_HOST_FILES_VERSION' }}</span>
      · <a :href="guide" target="_blank" rel="noopener" class="text-brand-purple">Host install guide</a>
    </span>
  </div>
</template>
```

`frontend/src/components/ServiceZone.vue`:
```vue
<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { applyUpgrade, checkNow, refreshUpgrades, useUpgrades, type ReleaseView } from '../composables/useUpgrades'
import HostUpdateSteps from './HostUpdateSteps.vue'

const POLL_MS = 5000
type Phase = 'idle' | 'applying' | 'restarting' | 'upgraded' | 'failed'

const { view, loadError } = useUpgrades()
const confirming = ref<string | null>(null)
const target = ref<string | null>(null)
const phase = ref<Phase>('idle')
const failure = ref('')
const actionError = ref<string | null>(null)
const checking = ref(false)
let timer: ReturnType<typeof setTimeout> | undefined

const pending = computed(() => (view.value?.releases ?? []).filter((r) => r.status === 'available' || r.status === 'applying'))
const history = computed(() => (view.value?.releases ?? []).filter((r) => r.status === 'applied' || r.status === 'failed'))
const busy = computed(() => phase.value === 'applying' || phase.value === 'restarting' || !!view.value?.in_flight)

function fmt(ts: string | null): string {
  return ts ? `${ts.slice(0, 16).replace('T', ' ')} UTC` : ''
}

function stopPolling(): void {
  if (timer !== undefined) clearTimeout(timer)
  timer = undefined
}

function schedule(): void {
  stopPolling()
  timer = setTimeout(() => { void poll() }, POLL_MS)
}

async function poll(): Promise<void> {
  const res = await refreshUpgrades()
  if (target.value === null) return
  if (res === 'unreachable') { phase.value = 'restarting'; schedule(); return }
  if (res !== 'ok' || !view.value) { schedule(); return }
  if (view.value.current === target.value) { phase.value = 'upgraded'; stopPolling(); return }
  const row = view.value.releases.find((r) => r.version === target.value)
  if (row?.status === 'failed') {
    phase.value = 'failed'
    failure.value = row.error
    stopPolling()
    return
  }
  phase.value = 'applying'
  schedule()
}

function begin(version: string): void {
  target.value = version
  failure.value = ''
  phase.value = 'applying'
  schedule()
}

onMounted(async () => {
  await refreshUpgrades()
  if (view.value?.in_flight) begin(view.value.in_flight)
})
onUnmounted(stopPolling)

async function onCheck(): Promise<void> {
  checking.value = true
  actionError.value = await checkNow()
  checking.value = false
}

async function onConfirm(r: ReleaseView): Promise<void> {
  confirming.value = null
  actionError.value = await applyUpgrade(r.version)
  if (actionError.value === null) begin(r.version)
}

function reload(): void {
  window.location.reload()
}
</script>

<template>
  <section data-testid="service-zone" class="rounded-[14px] border border-border bg-bg-card p-5 flex flex-col gap-4">
    <header class="flex flex-wrap items-start justify-between gap-3">
      <div class="flex flex-col gap-1">
        <h2 class="m-0 text-[16px] font-bold text-text-primary">Service</h2>
        <p class="m-0 text-[12.5px] text-text-muted">
          Running <span data-testid="current-version" class="font-mono text-text-primary">{{ view?.current ?? '…' }}</span>
          <template v-if="view?.checked_at"> · last checked <span data-testid="checked-at">{{ fmt(view.checked_at) }}</span></template>
        </p>
      </div>
      <button
        data-testid="check-now"
        :disabled="checking"
        class="px-[14px] py-2 rounded-[10px] border border-border bg-bg-card text-text-secondary text-[13px] font-semibold cursor-pointer disabled:opacity-50"
        @click="onCheck"
      >Check now</button>
    </header>

    <div v-if="loadError" data-testid="upgrades-error" class="text-[12.5px] text-fail">{{ loadError }}</div>
    <div v-if="actionError" data-testid="action-error" class="text-[12.5px] text-fail">{{ actionError }}</div>
    <div v-if="view && !view.apply_enabled" data-testid="apply-disabled" class="text-[12.5px] text-text-muted">
      Upgrades cannot be applied from this deployment: it is a development build, or UPGRADE_APPLY_DIR is not set.
    </div>

    <div v-if="phase !== 'idle'" data-testid="progress" role="status" class="flex flex-wrap items-center gap-2 rounded-[10px] border border-border bg-bg-app px-3 py-2 text-[13px] text-text-primary">
      <template v-if="phase === 'applying'">Upgrading to {{ target }}…</template>
      <template v-else-if="phase === 'restarting'">Restarting…</template>
      <template v-else-if="phase === 'upgraded'">
        Upgraded to {{ target }}.
        <button data-testid="reload" class="px-[10px] py-[4px] rounded-[8px] border border-border-strong bg-bg-card text-brand-purple text-[12px] font-semibold cursor-pointer" @click="reload">Reload</button>
      </template>
      <template v-else>Upgrade to {{ target }} failed: <span data-testid="failure" class="text-fail">{{ failure }}</span></template>
    </div>

    <ul v-if="pending.length" class="m-0 p-0 list-none flex flex-col divide-y divide-border border border-border rounded-[10px]">
      <li v-for="r in pending" :key="r.version" data-testid="release-row" class="flex flex-col gap-2 px-3 py-3">
        <div class="flex flex-wrap items-center gap-3">
          <a :href="r.url" target="_blank" rel="noopener" class="font-mono text-[13px] font-semibold text-brand-purple">{{ r.version }}</a>
          <span class="text-[11.5px] text-text-muted">{{ r.published_at.slice(0, 10) }}</span>
          <span v-if="r.status === 'applying'" class="ml-auto text-[11.5px] font-semibold text-warn">applying</span>
          <template v-else-if="confirming === r.version">
            <button data-testid="confirm" class="ml-auto px-[10px] py-[4px] rounded-[8px] border border-border-strong bg-bg-card text-brand-purple text-[12px] font-semibold cursor-pointer" @click="onConfirm(r)">Confirm upgrade to {{ r.version }}</button>
            <button data-testid="cancel" class="px-[10px] py-[4px] rounded-[8px] border border-border bg-bg-card text-text-secondary text-[12px] cursor-pointer" @click="confirming = null">Cancel</button>
          </template>
          <button
            v-else
            data-testid="upgrade"
            class="ml-auto px-[10px] py-[4px] rounded-[8px] border border-border-strong bg-bg-card text-brand-purple text-[12px] font-semibold cursor-pointer disabled:opacity-40 disabled:cursor-default"
            :disabled="!view?.apply_enabled || busy || r.blocked"
            @click="confirming = r.version"
          >{{ r.blocked ? 'Host update required' : 'Upgrade' }}</button>
        </div>
        <div v-if="r.blocked && view" data-testid="blocked-note" class="rounded-[8px] border border-warn bg-warn-tint p-3">
          <HostUpdateSteps :release="r" :repo="view.repo" :host-files-version="view.host_files_version" />
        </div>
        <div data-testid="notes" class="whitespace-pre-wrap text-[12.5px] text-text-secondary">{{ r.notes }}</div>
      </li>
    </ul>
    <div v-else-if="view" data-testid="up-to-date" class="text-[12.5px] text-text-muted">No newer release.</div>

    <div v-if="history.length" class="flex flex-col gap-1">
      <h3 class="m-0 text-[13px] font-semibold text-text-primary">History</h3>
      <ul class="m-0 p-0 list-none flex flex-col gap-1">
        <li v-for="r in history" :key="'h:' + r.version" data-testid="history-row" class="flex flex-wrap gap-2 text-[12px]">
          <span class="font-mono text-text-primary">{{ r.version }}</span>
          <span :class="r.status === 'failed' ? 'text-fail' : 'text-text-muted'">{{ r.status }}</span>
          <span class="text-text-muted">{{ fmt(r.finished_at) }}{{ r.requested_by ? ` · by ${r.requested_by}` : '' }}</span>
          <span v-if="r.error" class="text-fail">{{ r.error }}</span>
        </li>
      </ul>
    </div>
  </section>
</template>
```

In `SettingsPanel.vue`, add `import ServiceZone from './ServiceZone.vue'`, and make `<ServiceZone />` the first child of the outer `<div class="flex flex-col gap-5">`, above the Access `<section>`.

- [ ] **Step 4: Run the tests and the type check**

Run: `cd frontend && npm test && npx vue-tsc --noEmit`
Expected: every test passes, including the existing 66; no type errors.

- [ ] **Step 5: Commit**
```bash
git add frontend/src
git commit -s -m "feat(frontend): Settings Service zone with upgrades"
```

---

### Task 6: Header update dot and host-update banner

**Goal:** Signed-in users see a dot on Settings when a release is available. When the newest release is blocked, they see a non-dismissible amber banner with the manual host-update steps on every tab.

**Files:**
- Create: `frontend/src/components/HostUpdateBanner.vue`
- Test: `frontend/src/components/HostUpdateBanner.test.ts`
- Modify: `frontend/src/components/AppHeader.vue` (the dot)
- Modify: `frontend/src/components/AppHeader.test.ts` (dot tests)
- Modify: `frontend/src/App.vue` (start and stop the notices, render the banner)

**Acceptance Criteria:**
- [ ] `[data-testid="update-dot"]` is shown on the Settings pill only when the user is signed in and some release is `available`.
- [ ] `[data-testid="host-update-banner"]` (`role="alert"`) appears only when the user is signed in and the newest `available` release is `blocked`. It has no dismiss control. It reads "vX.Y.Z needs a host update before it can be installed from the UI." and shows `HostUpdateSteps`.
- [ ] `App.vue` calls `startUpgradeNotices()` when the user becomes signed in and `stopUpgradeNotices()` when they sign out.

**Verify:** `cd frontend && npx vitest run src/components/HostUpdateBanner.test.ts src/components/AppHeader.test.ts` → PASS

**Steps:**

- [ ] **Step 1: Write the failing tests**

`frontend/src/components/HostUpdateBanner.test.ts`:
```ts
// @vitest-environment jsdom
import { describe, it, expect, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import HostUpdateBanner from './HostUpdateBanner.vue'
import { useAuth, resetAuthForTests } from '../composables/useAuth'
import { useUpgrades, resetUpgradesForTests, type ReleaseView, type UpgradesView } from '../composables/useUpgrades'

function rel(over: Partial<ReleaseView>): ReleaseView {
  return {
    version: 'v0.3.0', notes: '', url: '', published_at: '2026-11-03T00:00:00Z', status: 'available',
    requested_by: '', requested_at: null, finished_at: null, error: '', host_changes: 'Add FOO=1',
    requires_host: 'v0.3.0', blocked: true, host_instructions: [{ version: 'v0.3.0', host_changes: 'Add FOO=1' }], ...over,
  }
}
function setView(releases: ReleaseView[]) {
  const v: UpgradesView = { current: 'v0.1.0', host_files_version: 'v0.1.0', repo: 'Percona-Lab/obs-dashboard', apply_enabled: true, in_flight: '', checked_at: null, releases }
  useUpgrades().view.value = v
}

beforeEach(() => { resetAuthForTests(); resetUpgradesForTests() })

describe('HostUpdateBanner', () => {
  it('shows the steps when the newest release is blocked', () => {
    useAuth().user.value = 'alice@percona.com'
    setView([rel({}), rel({ version: 'v0.2.0', blocked: false, requires_host: '', host_instructions: [] })])
    const w = mount(HostUpdateBanner)
    const b = w.find('[data-testid="host-update-banner"]')
    expect(b.exists()).toBe(true)
    expect(b.attributes('role')).toBe('alert')
    expect(b.text()).toContain('v0.3.0 needs a host update before it can be installed from the UI.')
    expect(b.text()).toContain('Add FOO=1')
    expect(w.find('button').exists()).toBe(false)
  })
  it('is hidden when signed out', () => {
    setView([rel({})])
    expect(mount(HostUpdateBanner).find('[data-testid="host-update-banner"]').exists()).toBe(false)
  })
  it('is hidden when the newest release is not blocked', () => {
    useAuth().user.value = 'alice@percona.com'
    setView([rel({ blocked: false }), rel({ version: 'v0.2.0' })])
    expect(mount(HostUpdateBanner).find('[data-testid="host-update-banner"]').exists()).toBe(false)
  })
})
```

Append to `frontend/src/components/AppHeader.test.ts`:
```ts
import { useUpgrades, resetUpgradesForTests } from '../composables/useUpgrades'

describe('AppHeader update dot', () => {
  beforeEach(() => resetUpgradesForTests())
  const v = (status: 'available' | 'applied') => ({
    current: 'v0.1.0', host_files_version: '', repo: 'Percona-Lab/obs-dashboard', apply_enabled: true, in_flight: '', checked_at: null,
    releases: [{ version: 'v0.2.0', notes: '', url: '', published_at: '', status, requested_by: '', requested_at: null, finished_at: null, error: '', host_changes: '', requires_host: '', blocked: false, host_instructions: [] }],
  })
  it('shows a dot on Settings when a release is available', () => {
    useAuth().user.value = 'alice@percona.com'
    useAuth().authEnabled.value = true
    useUpgrades().view.value = v('available')
    expect(mountHeader().find('[data-testid="update-dot"]').exists()).toBe(true)
  })
  it('shows no dot when nothing is available', () => {
    useAuth().user.value = 'alice@percona.com'
    useUpgrades().view.value = v('applied')
    expect(mountHeader().find('[data-testid="update-dot"]').exists()).toBe(false)
  })
})
```
Move the imports to the top of the file to match its style. If `authEnabled` isn't exposed as writable by `useAuth()`, drop that line, because the dot only depends on `signedIn`.

- [ ] **Step 2: Run the tests to confirm they fail**

Run: `cd frontend && npx vitest run src/components/HostUpdateBanner.test.ts src/components/AppHeader.test.ts`
Expected: FAIL (the module is missing, and there's no `update-dot`).

- [ ] **Step 3: Implement**

`frontend/src/components/HostUpdateBanner.vue`:
```vue
<script setup lang="ts">
import { useAuth } from '../composables/useAuth'
import { useUpgrades } from '../composables/useUpgrades'
import HostUpdateSteps from './HostUpdateSteps.vue'

const { signedIn } = useAuth()
const { view, blockedRelease } = useUpgrades()
</script>

<template>
  <div
    v-if="signedIn && view && blockedRelease"
    role="alert"
    data-testid="host-update-banner"
    class="flex flex-col gap-3 px-4 py-3 rounded-[10px] border-2 border-warn bg-warn-tint"
  >
    <span class="text-[14px] font-bold text-text-primary">{{ blockedRelease.version }} needs a host update before it can be installed from the UI.</span>
    <HostUpdateSteps :release="blockedRelease" :repo="view.repo" :host-files-version="view.host_files_version" />
  </div>
</template>
```

In `AppHeader.vue`:
- Add `import { useUpgrades } from '../composables/useUpgrades'` and `const { updateAvailable } = useUpgrades()`.
- Inside the Settings `<button …>`, after the text `Settings`, add:
```vue
        <span v-if="updateAvailable" data-testid="update-dot" class="ml-[6px] inline-block w-[7px] h-[7px] rounded-full bg-warn align-middle" aria-label="Update available"></span>
```

In `App.vue`:
- Import `watch` from vue if it isn't already imported. Add `import HostUpdateBanner from './components/HostUpdateBanner.vue'` and `import { startUpgradeNotices, stopUpgradeNotices } from './composables/useUpgrades'`.
- After `guardSettingsTab(mainTab)`, add:
```ts
// Upgrade notices (header dot, host-update banner) are for signed-in users.
watch(signedIn, (on) => (on ? startUpgradeNotices() : stopUpgradeNotices()), { immediate: true })
```
- In the template, right after the `v-if="notice"` status div, add `<HostUpdateBanner />`.

- [ ] **Step 4: Run all frontend tests, the type check and the build**

Run: `cd frontend && npm test && npm run build`
Expected: all tests pass; the build succeeds.

- [ ] **Step 5: Commit**
```bash
git add frontend/src
git commit -s -m "feat(frontend): update dot and host-update banner"
```

---

### Task 7: Host files (`deploy/host/`) with tests and CI

**Goal:** Ship the Quadlet unit, `apply.sh`, the apply units and the manual guide. They are verified by a stub-based test suite, shellcheck, `systemd-analyze` and a Quadlet dry-run in CI.

**Files:**
- Create: `deploy/host/obs-dashboard.container`
- Create: `deploy/host/obs-dashboard-apply.path`
- Create: `deploy/host/obs-dashboard-apply.service`
- Create: `deploy/host/apply.sh` (mode 0755)
- Create: `deploy/host/README.md`
- Create: `deploy/host/test/run.sh`, `deploy/host/test/quadlet-check.sh` (0755)
- Create: `deploy/host/test/stubs/podman`, `deploy/host/test/stubs/systemctl`, `deploy/host/test/stubs/curl` (0755)
- Modify: `.github/workflows/ci.yml` (a `host-scripts` job)

**Acceptance Criteria:**
- [ ] `deploy/host/test/run.sh` passes all of these cases:
  - success;
  - invalid version;
  - backup name with path traversal;
  - missing backup;
  - pull failure (pin unchanged, no restart);
  - new version never healthy → rollback (pin, DB restored, `-wal` removed, previous running, error ends with "rolled back to v0.1.0");
  - new version reports the wrong version → rollback;
  - rollback start fails → error contains "rollback failed";
  - no request → no status written.
- [ ] Every case that has a request leaves exactly one `apply-status` and no `apply-request`.
- [ ] `shellcheck` is clean on `apply.sh`, the test scripts and the stubs.
- [ ] `systemd-analyze verify` passes on the `.path` and `.service` units.
- [ ] `quadlet-check.sh` generates a service whose `ExecStart` runs the pinned image, publishes `127.0.0.1:4000:4000` and mounts `/data` and `/apply`.
- [ ] No file under `deploy/host/` names a deployment tool or an internal host.

**Verify:** `deploy/host/test/run.sh && shellcheck deploy/host/apply.sh deploy/host/test/*.sh deploy/host/test/stubs/*` → `all N cases passed`, no shellcheck output

**Steps:**

- [ ] **Step 1: Write the test harness first.**

`deploy/host/test/stubs/podman`:
```bash
#!/usr/bin/env bash
# Test stub: records calls; STUB_PULL_FAIL=1 makes `pull` fail.
echo "podman $*" >>"$STUB_CALLS"
if [[ $1 == pull && ${STUB_PULL_FAIL:-0} == 1 ]]; then
  echo "Error: manifest unknown" >&2
  exit 125
fi
exit 0
```

`deploy/host/test/stubs/systemctl`:
```bash
#!/usr/bin/env bash
# Test stub: start/restart "runs" the version pinned in $DROPIN; stop clears it.
# STUB_START_FAIL=1 makes `start` (used by rollback) fail.
echo "systemctl $*" >>"$STUB_CALLS"
case $1 in
  start | restart)
    if [[ $1 == start && ${STUB_START_FAIL:-0} == 1 ]]; then exit 1; fi
    sed -n 's|^Image=.*:\(v[0-9][0-9.]*\)$|\1|p' "$DROPIN" >"$STUB_DIR/running"
    ;;
  stop) rm -f "$STUB_DIR/running" ;;
esac
exit 0
```

`deploy/host/test/stubs/curl`:
```bash
#!/usr/bin/env bash
# Test stub for the /healthz probe. STUB_HEALTH: ok (default), baddead (the
# STUB_BAD_VERSION never answers), badwrong (it reports v0.0.1).
echo "curl $*" >>"$STUB_CALLS"
[[ -s $STUB_DIR/running ]] || exit 7
v=$(cat "$STUB_DIR/running")
if [[ $v == "${STUB_BAD_VERSION:-}" ]]; then
  case ${STUB_HEALTH:-ok} in
    baddead) exit 7 ;;
    badwrong) v=v0.0.1 ;;
  esac
fi
printf '{"status":"ok","version":"%s"}\n' "$v"
```

`deploy/host/test/run.sh`:
```bash
#!/usr/bin/env bash
# Tests apply.sh against stub podman/systemctl/curl in a temp tree.
set -euo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
APPLY="$HERE/../apply.sh"
BACKUP=pre-v0.2.0-20261106T120000Z.db
pass=0
failures=()

setup() {
  T=$(mktemp -d)
  export STUB_DIR="$T/stub" STUB_CALLS="$T/calls"
  export APPLY_DIR="$T/apply" DATA_DIR="$T/data"
  export DROPIN="$T/units/obs-dashboard.container.d/10-version.conf"
  export PODMAN="$HERE/stubs/podman" SYSTEMCTL="$HERE/stubs/systemctl" CURL="$HERE/stubs/curl"
  export HEALTH_TIMEOUT=1 HEALTH_INTERVAL=0.1
  unset STUB_PULL_FAIL STUB_START_FAIL STUB_HEALTH STUB_BAD_VERSION
  mkdir -p "$STUB_DIR" "$APPLY_DIR" "$DATA_DIR/backups" "$(dirname "$DROPIN")"
  printf '[Container]\nImage=ghcr.io/percona-lab/obs-dashboard:v0.1.0\n' >"$DROPIN"
  echo current-db >"$DATA_DIR/obsboard.db"
  echo wal >"$DATA_DIR/obsboard.db-wal"
  echo backup-db >"$DATA_DIR/backups/$BACKUP"
  echo v0.1.0 >"$STUB_DIR/running"
  : >"$STUB_CALLS"
}

request() { # <version> <backup>
  printf '{"version":"%s","previous":"v0.1.0","backup":"%s","requested_by":"a@percona.com","requested_at":"2026-11-06T12:00:00Z"}\n' \
    "$1" "$2" >"$APPLY_DIR/apply-request"
}

run_apply() { bash "$APPLY" >"$T/out" 2>&1; }
st() { jq -r "$1" "$APPLY_DIR/apply-status"; }
pin() { sed -n 's|^Image=.*:\(v[0-9.]*\)$|\1|p' "$DROPIN"; }
ok() { # <description> <command...>
  local d=$1
  shift
  if "$@"; then return 0; fi
  echo "    expected: $d"
  return 1
}
one_status() { [[ -f $APPLY_DIR/apply-status && ! -e $APPLY_DIR/apply-request ]]; }

case_success() {
  request v0.2.0 "$BACKUP"
  run_apply
  ok "one status, request removed" one_status &&
    ok "ok=true" [ "$(st .ok)" = true ] &&
    ok "version" [ "$(st .version)" = v0.2.0 ] &&
    ok "previous" [ "$(st .previous)" = v0.1.0 ] &&
    ok "pin v0.2.0" [ "$(pin)" = v0.2.0 ] &&
    ok "db untouched" [ "$(cat "$DATA_DIR/obsboard.db")" = current-db ] &&
    ok "pulled" grep -q 'podman pull ghcr.io/percona-lab/obs-dashboard:v0.2.0' "$STUB_CALLS" &&
    ok "restarted" grep -q 'systemctl restart obs-dashboard.service' "$STUB_CALLS" &&
    ok "pruned" grep -q 'podman image prune -f' "$STUB_CALLS" &&
    ok "no .prev left" [ ! -e "$DROPIN.prev" ]
}

case_invalid_version() {
  request 'v0.2;rm -rf /' "$BACKUP"
  run_apply
  ok "one status" one_status &&
    ok "ok=false" [ "$(st .ok)" = false ] &&
    ok "error" grep -q 'invalid version' <<<"$(st .error)" &&
    ok "no podman" bash -c "! grep -q podman '$STUB_CALLS'" &&
    ok "pin unchanged" [ "$(pin)" = v0.1.0 ]
}

case_backup_traversal() {
  request v0.2.0 '../obsboard.db'
  run_apply
  ok "one status" one_status && ok "error" grep -q 'invalid backup' <<<"$(st .error)" && ok "pin unchanged" [ "$(pin)" = v0.1.0 ]
}

case_missing_backup() {
  request v0.2.0 pre-v0.2.0-20261106T130000Z.db
  run_apply
  ok "one status" one_status && ok "error" grep -q 'backup not found' <<<"$(st .error)"
}

case_pull_failure() {
  export STUB_PULL_FAIL=1
  request v0.2.0 "$BACKUP"
  run_apply
  ok "one status" one_status &&
    ok "ok=false" [ "$(st .ok)" = false ] &&
    ok "error" grep -q 'pull failed' <<<"$(st .error)" &&
    ok "pin unchanged" [ "$(pin)" = v0.1.0 ] &&
    ok "no restart" bash -c "! grep -q 'systemctl restart' '$STUB_CALLS'"
}

assert_rolled_back() {
  ok "one status" one_status &&
    ok "ok=false" [ "$(st .ok)" = false ] &&
    ok "rolled back message" grep -q 'rolled back to v0.1.0$' <<<"$(st .error)" &&
    ok "pin restored" [ "$(pin)" = v0.1.0 ] &&
    ok "db restored" [ "$(cat "$DATA_DIR/obsboard.db")" = backup-db ] &&
    ok "wal removed" [ ! -e "$DATA_DIR/obsboard.db-wal" ] &&
    ok "previous running" [ "$(cat "$STUB_DIR/running")" = v0.1.0 ] &&
    ok "no .prev left" [ ! -e "$DROPIN.prev" ]
}

case_health_timeout() {
  export STUB_HEALTH=baddead STUB_BAD_VERSION=v0.2.0
  request v0.2.0 "$BACKUP"
  run_apply
  assert_rolled_back
}

case_wrong_version() {
  export STUB_HEALTH=badwrong STUB_BAD_VERSION=v0.2.0
  request v0.2.0 "$BACKUP"
  run_apply
  assert_rolled_back
}

case_rollback_fails() {
  export STUB_HEALTH=baddead STUB_BAD_VERSION=v0.2.0 STUB_START_FAIL=1
  request v0.2.0 "$BACKUP"
  run_apply
  ok "one status" one_status && ok "rollback failed" grep -q 'rollback failed' <<<"$(st .error)"
}

case_no_request() {
  run_apply
  ok "no status" [ ! -e "$APPLY_DIR/apply-status" ]
}

for c in success invalid_version backup_traversal missing_backup pull_failure health_timeout wrong_version rollback_fails no_request; do
  setup
  if "case_$c"; then
    pass=$((pass + 1))
    echo "ok   $c"
  else
    failures+=("$c")
    echo "FAIL $c"
    sed 's/^/    | /' "$T/out"
  fi
  rm -rf "$T"
done

if ((${#failures[@]})); then
  echo "${#failures[@]} case(s) failed: ${failures[*]}"
  exit 1
fi
echo "all $pass cases passed"
```

Make them executable: `chmod 0755 deploy/host/test/run.sh deploy/host/test/stubs/*`.

- [ ] **Step 2: Run the harness to confirm it fails**

Run: `deploy/host/test/run.sh`
Expected: FAIL. `apply.sh` doesn't exist, so `bash` errors out in every case except `no_request`, which may pass by accident.

- [ ] **Step 3: Implement the host files**

`deploy/host/apply.sh` (mode 0755):
```bash
#!/usr/bin/env bash
# Applies an obs-dashboard upgrade requested from the dashboard UI.
#
# Run by obs-dashboard-apply.service when the dashboard writes
# $APPLY_DIR/apply-request. Pulls the requested image, re-pins the Quadlet
# drop-in, restarts the service and waits for /healthz to report the new
# version. On failure after the switch it restores the previous pin and the
# pre-upgrade database backup. Always writes exactly one apply-status and
# removes the request. Every request field is untrusted; the image repository
# is fixed here and never read from the request.
#
# All paths and commands can be overridden through the environment for tests.
set -euo pipefail

APPLY_DIR=${APPLY_DIR:-/etc/obs-dashboard/apply}
DATA_DIR=${DATA_DIR:-/var/lib/obs-dashboard}
DROPIN=${DROPIN:-/etc/containers/systemd/obs-dashboard.container.d/10-version.conf}
UNIT=${UNIT:-obs-dashboard.service}
IMAGE_REPO=${IMAGE_REPO:-ghcr.io/percona-lab/obs-dashboard}
HEALTH_URL=${HEALTH_URL:-http://127.0.0.1:4000/healthz}
HEALTH_TIMEOUT=${HEALTH_TIMEOUT:-120}
HEALTH_INTERVAL=${HEALTH_INTERVAL:-2}
PODMAN=${PODMAN:-podman}
SYSTEMCTL=${SYSTEMCTL:-systemctl}
CURL=${CURL:-curl}

REQ="$APPLY_DIR/apply-request"
STATUS="$APPLY_DIR/apply-status"
VERSION_RE='^v[0-9]+\.[0-9]+\.[0-9]+$'
BACKUP_RE='^pre-v[0-9]+\.[0-9]+\.[0-9]+-[0-9]{8}T[0-9]{6}Z\.db$'

VERSION=""
PREVIOUS=""
BACKUP=""
SWITCHED=0
DONE=0

log() { echo "apply: $*"; }

write_status() { # <true|false> <error>
  local tmp
  tmp=$(mktemp "$APPLY_DIR/.apply-status.XXXXXX")
  jq -n --arg v "$VERSION" --arg p "$PREVIOUS" --argjson ok "$1" --arg e "$2" \
    --arg t "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    '{version: $v, previous: $p, ok: $ok, error: $e, finished_at: $t}' >"$tmp"
  chmod 0644 "$tmp"
  mv -f "$tmp" "$STATUS"
  rm -f "$REQ"
  DONE=1
  if [[ $1 == true ]]; then log "done: running $VERSION"; else log "failed: $2"; fi
}

fail() { # <error>: report a failure before anything was switched
  write_status false "$1"
  exit 0
}

pinned_version() {
  sed -n 's|^Image=.*:\(v[0-9][0-9.]*\)[[:space:]]*$|\1|p' "$DROPIN" | tail -n 1
}

write_pin() { # <version>
  local tmp
  tmp=$(mktemp "$(dirname "$DROPIN")/.10-version.XXXXXX")
  printf '[Container]\nImage=%s:%s\n' "$IMAGE_REPO" "$1" >"$tmp"
  chmod 0644 "$tmp"
  mv -f "$tmp" "$DROPIN"
}

wait_healthy() { # <version>
  local deadline=$((SECONDS + HEALTH_TIMEOUT)) body got
  while ((SECONDS < deadline)); do
    if body=$("$CURL" -fsS --max-time 5 "$HEALTH_URL" 2>/dev/null); then
      got=$(jq -r '.version // empty' <<<"$body" 2>/dev/null || true)
      [[ $got == "$1" ]] && return 0
    fi
    sleep "$HEALTH_INTERVAL"
  done
  return 1
}

rollback() { # <reason>
  set +e
  local problems=() db="$DATA_DIR/obsboard.db" stopped=1
  log "rolling back to $PREVIOUS: $1"
  "$SYSTEMCTL" stop "$UNIT" || { problems+=("stop failed"); stopped=0; }
  if ((stopped)); then
    if cp -f "$DATA_DIR/backups/$BACKUP" "$db.restore" && mv -f "$db.restore" "$db"; then
      rm -f "$db-wal" "$db-shm"
    else
      problems+=("database restore failed")
    fi
  fi
  if [[ -f $DROPIN.prev ]]; then
    mv -f "$DROPIN.prev" "$DROPIN" || problems+=("pin restore failed")
  fi
  "$SYSTEMCTL" daemon-reload || problems+=("daemon-reload failed")
  if ! "$SYSTEMCTL" start "$UNIT"; then
    problems+=("start failed")
  elif ! wait_healthy "$PREVIOUS"; then
    problems+=("$PREVIOUS not healthy within ${HEALTH_TIMEOUT}s")
  fi
  if ((${#problems[@]})); then
    local joined
    joined=$(printf '%s, ' "${problems[@]}")
    write_status false "$1; rollback failed: ${joined%, }"
  else
    write_status false "$1; rolled back to $PREVIOUS"
  fi
}

on_exit() {
  local rc=$?
  ((DONE)) && return
  if ((SWITCHED)); then
    rollback "apply.sh failed unexpectedly (exit $rc)"
  else
    write_status false "apply.sh failed unexpectedly (exit $rc)"
  fi
}

if [[ ! -f $REQ ]]; then
  log "no apply-request; nothing to do"
  exit 0
fi
trap on_exit EXIT

VERSION=$(jq -r '.version // "" | tostring' "$REQ" 2>/dev/null) || VERSION=""
BACKUP=$(jq -r '.backup // "" | tostring' "$REQ" 2>/dev/null) || BACKUP=""
[[ -f $DROPIN ]] || fail "version drop-in $DROPIN is missing"
PREVIOUS=$(pinned_version)
[[ $PREVIOUS =~ $VERSION_RE ]] || fail "cannot read the running version from $DROPIN"
[[ $VERSION =~ $VERSION_RE ]] || fail "invalid version in request: '$VERSION'"
[[ $BACKUP =~ $BACKUP_RE ]] || fail "invalid backup name in request: '$BACKUP'"
[[ -f $DATA_DIR/backups/$BACKUP ]] || fail "backup not found: $BACKUP"
[[ $VERSION != "$PREVIOUS" ]] || fail "$VERSION is already running"

log "upgrading $PREVIOUS -> $VERSION (backup $BACKUP)"
"$PODMAN" pull "$IMAGE_REPO:$VERSION" || fail "image pull failed for $IMAGE_REPO:$VERSION; still running $PREVIOUS"

cp -p "$DROPIN" "$DROPIN.prev"
SWITCHED=1
write_pin "$VERSION"
"$SYSTEMCTL" daemon-reload
"$SYSTEMCTL" restart "$UNIT"
if ! wait_healthy "$VERSION"; then
  rollback "$VERSION did not report healthy within ${HEALTH_TIMEOUT}s"
  exit 0
fi
rm -f "$DROPIN.prev"
"$PODMAN" image prune -f >/dev/null 2>&1 || true
write_status true ""
```

`deploy/host/obs-dashboard.container`:
```ini
# obs-dashboard Quadlet unit; generates obs-dashboard.service.
# Install to /etc/containers/systemd/. Site values live in /etc/obs-dashboard/env.
[Unit]
Description=OBS Dashboard
Wants=network-online.target
After=network-online.target

[Container]
ContainerName=obs-dashboard
# Image= lives in obs-dashboard.container.d/10-version.conf (written once at
# install, rewritten only by apply.sh).
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

`deploy/host/obs-dashboard-apply.path`:
```ini
[Unit]
Description=Watch for obs-dashboard upgrade requests

[Path]
PathExists=/etc/obs-dashboard/apply/apply-request
Unit=obs-dashboard-apply.service

[Install]
WantedBy=paths.target
```

`deploy/host/obs-dashboard-apply.service`:
```ini
[Unit]
Description=Apply a requested obs-dashboard upgrade
StartLimitIntervalSec=300
StartLimitBurst=3

[Service]
Type=oneshot
User=root
ExecStart=/usr/local/lib/obs-dashboard/apply.sh
TimeoutStartSec=900
```

`deploy/host/test/quadlet-check.sh` (mode 0755):
```bash
#!/usr/bin/env bash
# Dry-runs the Quadlet generator on obs-dashboard.container with a sample pin.
set -euo pipefail
here=$(cd "$(dirname "$0")/.." && pwd)
q=""
for c in /usr/libexec/podman/quadlet /usr/lib/podman/quadlet; do
  if [[ -x $c ]]; then q=$c; break; fi
done
[[ -n $q ]] || { echo "quadlet generator not found (install podman)" >&2; exit 1; }
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/units"
# Inline the pin the drop-in would provide, so the check does not depend on
# the CI podman's drop-in support.
sed '/^\[Container\]/a Image=ghcr.io/percona-lab/obs-dashboard:v0.0.0' \
  "$here/obs-dashboard.container" >"$work/units/obs-dashboard.container"
QUADLET_UNIT_DIRS="$work/units" "$q" -dryrun >"$work/out" 2>"$work/err" || { cat "$work/err" >&2; exit 1; }
for want in 'ghcr.io/percona-lab/obs-dashboard:v0.0.0' '127.0.0.1:4000:4000' ':/data' ':/apply' 'UPGRADE_APPLY_DIR=/apply'; do
  grep -q -- "$want" "$work/out" || { echo "generated unit lacks: $want" >&2; cat "$work/out" >&2; exit 1; }
done
echo "quadlet: OK"
```

`deploy/host/README.md`:
````markdown
# Host files

These files let a host run obs-dashboard as a Podman Quadlet service, and
apply upgrades requested from **Settings → Service**. They are attached to
every GitHub release as `obs-dashboard-host-vX.Y.Z.tar.gz`, with a
`SHA256SUMS` file.

| File | Installed to |
|---|---|
| `obs-dashboard.container` | `/etc/containers/systemd/` |
| `apply.sh` | `/usr/local/lib/obs-dashboard/apply.sh` (0755, root) |
| `obs-dashboard-apply.path`, `obs-dashboard-apply.service` | `/etc/systemd/system/` |

## Prerequisites

- Linux with systemd, and Podman with Quadlet support.
- `jq` and `curl`.
- A reverse proxy that terminates TLS for the public URL and forwards to
  `127.0.0.1:4000`. Turn response buffering off and use a long read timeout:
  `/api/stream` is a Server-Sent Events stream.

## Layout

| Path | Purpose |
|---|---|
| `/etc/obs-dashboard/env` (0600) | Secrets and site values (see below) |
| `/etc/obs-dashboard/config.yaml` | Dashboard config (see `config.yaml.example`) |
| `/etc/obs-dashboard/apply/` | Upgrade handshake directory, mounted at `/apply` |
| `/var/lib/obs-dashboard/` | Database and pre-upgrade backups, mounted at `/data` |
| `/etc/containers/systemd/obs-dashboard.container.d/10-version.conf` | The running version pin |

`env` must contain:

```sh
AUTH_PUBLIC_URL=https://dashboard.example.com
OBS_DASHBOARD_HOST_FILES_VERSION=vX.Y.Z   # the host-files version installed
GOOGLE_CLIENT_ID=...
GOOGLE_CLIENT_SECRET=...
OBS_DASHBOARD_ADMINS=someone@percona.com
# plus per-instance OBS credentials, e.g. OBS_<SLUG>_USERNAME / _PASSWORD
```

## First install (as root)

```sh
v=vX.Y.Z
cd "$(mktemp -d)"
curl -fLO https://github.com/Percona-Lab/obs-dashboard/releases/download/$v/obs-dashboard-host-$v.tar.gz
curl -fLO https://github.com/Percona-Lab/obs-dashboard/releases/download/$v/SHA256SUMS
sha256sum -c --ignore-missing SHA256SUMS
tar xzf obs-dashboard-host-$v.tar.gz
install -d -m 0755 /etc/obs-dashboard /var/lib/obs-dashboard
install -d -m 0770 /etc/obs-dashboard/apply
install -m 0644 obs-dashboard.container /etc/containers/systemd/
install -D -m 0755 apply.sh /usr/local/lib/obs-dashboard/apply.sh
install -m 0644 obs-dashboard-apply.path obs-dashboard-apply.service /etc/systemd/system/
install -d /etc/containers/systemd/obs-dashboard.container.d
printf '[Container]\nImage=ghcr.io/percona-lab/obs-dashboard:%s\n' "$v" \
  > /etc/containers/systemd/obs-dashboard.container.d/10-version.conf
# write /etc/obs-dashboard/env (0600) and /etc/obs-dashboard/config.yaml
systemctl daemon-reload
systemctl enable --now obs-dashboard-apply.path
systemctl start obs-dashboard
curl -fsS http://127.0.0.1:4000/healthz
```

Write the version drop-in **only at first install**. After that, only
`apply.sh` rewrites it. A configuration-management tool must not overwrite
an existing drop-in, or it will revert upgrades applied from the UI.

## Updating the host files

A release whose changelog has a **Host changes** section needs new host
files before it can be applied from the UI. The dashboard shows a banner
with these steps until `OBS_DASHBOARD_HOST_FILES_VERSION` is high enough.

```sh
v=vX.Y.Z   # the release that introduced the host changes
cd "$(mktemp -d)"
curl -fLO https://github.com/Percona-Lab/obs-dashboard/releases/download/$v/obs-dashboard-host-$v.tar.gz
curl -fLO https://github.com/Percona-Lab/obs-dashboard/releases/download/$v/SHA256SUMS
sha256sum -c --ignore-missing SHA256SUMS
tar xzf obs-dashboard-host-$v.tar.gz
install -m 0644 obs-dashboard.container /etc/containers/systemd/
install -D -m 0755 apply.sh /usr/local/lib/obs-dashboard/apply.sh
install -m 0644 obs-dashboard-apply.path obs-dashboard-apply.service /etc/systemd/system/
# apply the release-specific host changes from the changelog, then:
grep -q '^OBS_DASHBOARD_HOST_FILES_VERSION=' /etc/obs-dashboard/env \
  && sed -i "s/^OBS_DASHBOARD_HOST_FILES_VERSION=.*/OBS_DASHBOARD_HOST_FILES_VERSION=$v/" /etc/obs-dashboard/env \
  || echo "OBS_DASHBOARD_HOST_FILES_VERSION=$v" >> /etc/obs-dashboard/env
systemctl daemon-reload
systemctl enable --now obs-dashboard-apply.path
systemctl restart obs-dashboard
```

If the host is managed by a deployment tool, make the equivalent change
there, so the next run does not revert it.

## How an upgrade is applied

1. A signed-in user clicks **Upgrade** in Settings.
2. The dashboard backs up its database to
   `/var/lib/obs-dashboard/backups/pre-<version>-<timestamp>.db`, keeping the
   newest 5 backups. It then writes `apply/apply-request`.
3. `obs-dashboard-apply.path` starts `apply.sh`, which:
   1. pulls the image;
   2. re-pins the drop-in, reloads systemd and restarts `obs-dashboard`;
   3. waits up to 120s for `/healthz` to report the new version.
4. On failure it restores the previous pin and the database backup, and
   restarts the previous version. It always writes `apply/apply-status`,
   which the dashboard reads and shows.
5. Logs: `journalctl -u obs-dashboard-apply`.

## Manual rollback to an earlier version

The UI never downgrades. To go back to an earlier version yourself:

```sh
systemctl stop obs-dashboard
cp /var/lib/obs-dashboard/backups/pre-<version-you-left>-<ts>.db /var/lib/obs-dashboard/obsboard.db
rm -f /var/lib/obs-dashboard/obsboard.db-wal /var/lib/obs-dashboard/obsboard.db-shm
printf '[Container]\nImage=ghcr.io/percona-lab/obs-dashboard:%s\n' vX.Y.Z \
  > /etc/containers/systemd/obs-dashboard.container.d/10-version.conf
systemctl daemon-reload
systemctl start obs-dashboard
```

Restoring a backup loses everything recorded after that backup was taken.
````

Make the scripts executable: `chmod 0755 deploy/host/apply.sh deploy/host/test/quadlet-check.sh`.

- [ ] **Step 4: Run the tests and linters locally**

Run: `deploy/host/test/run.sh && shellcheck deploy/host/apply.sh deploy/host/test/*.sh deploy/host/test/stubs/*`
Expected: `all 9 cases passed`, and no shellcheck output. If shellcheck isn't installed locally, run it in Docker: `docker run --rm -v "$PWD:/mnt" -w /mnt koalaman/shellcheck:stable deploy/host/apply.sh deploy/host/test/*.sh deploy/host/test/stubs/*`. If podman is installed locally, also run `deploy/host/test/quadlet-check.sh`; otherwise CI runs it.

- [ ] **Step 5: Add the CI job.** Append to `.github/workflows/ci.yml` under `jobs:`:
```yaml
  host-scripts:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Shellcheck
        run: shellcheck deploy/host/apply.sh deploy/host/test/*.sh deploy/host/test/stubs/*

      - name: Verify systemd units
        run: |
          sudo install -D -m 0755 deploy/host/apply.sh /usr/local/lib/obs-dashboard/apply.sh
          systemd-analyze verify deploy/host/obs-dashboard-apply.path deploy/host/obs-dashboard-apply.service

      - name: Verify Quadlet unit
        run: deploy/host/test/quadlet-check.sh

      - name: Apply script tests
        run: deploy/host/test/run.sh
```
Confirm that no deployment tool or internal host is named anywhere in `deploy/host` or `.github/workflows/ci.yml`.

- [ ] **Step 6: Commit**
```bash
git add deploy/host .github/workflows/ci.yml
git commit -s -m "feat(deploy): host files for UI upgrades with tests"
```

---

### Task 8: Release tooling and workflow

**Goal:** `task release VERSION=X.Y.Z` cuts a signed-off release commit and tag. A pushed `v*` tag then verifies the release, publishes the image, and creates a GitHub release with the changelog notes and the host-files tarball plus `SHA256SUMS`.

**Files:**
- Create: `CHANGELOG.md`
- Create: `scripts/changelog-section.sh`, `scripts/check-release.sh`, `scripts/release.sh` (0755)
- Create: `scripts/test/run.sh` (0755)
- Create: `.github/workflows/release.yml`
- Modify: `.github/workflows/ci.yml` (run `scripts/test/run.sh` in the `host-scripts` job)
- Modify: `Taskfile.yml` (`release` task)

**Acceptance Criteria:**
- [ ] `changelog-section.sh vX.Y.Z` prints that section's body with surrounding blank lines trimmed, and prints nothing when the section is absent. `Unreleased` also works as the version.
- [ ] `check-release.sh vX.Y.Z` fails when:
  - the section is missing or empty;
  - `deploy/host/` (excluding `test/`) changed since the previous `v*` tag without a `### Host changes` subsection (heading level 2–4, case-insensitive);
  - this is the first tag, `deploy/host/` exists, and the subsection is missing.

  It passes when only `deploy/host/test/` changed.
- [ ] `release.sh X.Y.Z` refuses to run with a dirty tree, an existing tag, or an empty `Unreleased` section. Otherwise it:
  - moves `Unreleased` into `## [X.Y.Z] - YYYY-MM-DD`;
  - bumps `frontend/package.json` and the lock file;
  - commits with `-s`;
  - creates the annotated tag `vX.Y.Z`;
  - runs `check-release.sh`;
  - never pushes.
- [ ] `release.yml` runs `verify` (release check, Go tests, frontend tests and build, host-script checks), then `image` (pushes `ghcr.io/percona-lab/obs-dashboard:<tag>` for linux/amd64 with `VERSION=<tag>`), then `release` (`gh release create` with the notes and `obs-dashboard-host-<tag>.tar.gz` plus `SHA256SUMS`). The tarball excludes `test/`.

**Verify:** `scripts/test/run.sh` → `all N cases passed`

**Steps:**

- [ ] **Step 1: Write the failing script tests** `scripts/test/run.sh`:
```bash
#!/usr/bin/env bash
# Tests changelog-section.sh and check-release.sh in throwaway git repos.
set -euo pipefail
SCRIPTS=$(cd "$(dirname "$0")/.." && pwd)
pass=0
failures=()

repo() { # fresh repo with scripts and a deploy/host file
  R=$(mktemp -d)
  git -C "$R" init -q
  git -C "$R" config user.email t@example.com
  git -C "$R" config user.name t
  mkdir -p "$R/scripts" "$R/deploy/host/test"
  cp "$SCRIPTS/changelog-section.sh" "$SCRIPTS/check-release.sh" "$R/scripts/"
  echo a >"$R/deploy/host/apply.sh"
  echo t >"$R/deploy/host/test/run.sh"
}
commit_tag() { # <tag> <changelog content>
  printf '%s\n' "$2" >"$R/CHANGELOG.md"
  git -C "$R" add -A
  git -C "$R" commit -q -m "$1"
  git -C "$R" tag -a "$1" -m "$1"
}
check() { (cd "$R" && scripts/check-release.sh "$1") >/dev/null 2>&1; }

CL1='# Changelog

## [Unreleased]

## [0.1.0] - 2026-11-06

### Added

- first

### Host changes

- install host files
'

case_section() {
  repo
  printf '%s' "$CL1" >"$R/CHANGELOG.md"
  local out
  out=$(cd "$R" && scripts/changelog-section.sh v0.1.0)
  [[ $(head -n 1 <<<"$out") == "### Added" && $(tail -n 1 <<<"$out") == "- install host files" ]] &&
    [[ -z $(cd "$R" && scripts/changelog-section.sh v9.9.9) ]]
}
case_first_tag_ok() { repo; commit_tag v0.1.0 "$CL1"; check v0.1.0; }
case_first_tag_needs_host_changes() {
  repo
  commit_tag v0.1.0 '# Changelog

## [0.1.0] - 2026-11-06

- first'
  ! check v0.1.0
}
case_missing_section() { repo; commit_tag v0.1.0 '# Changelog'; ! check v0.1.0; }
case_host_change_without_note() {
  repo
  commit_tag v0.1.0 "$CL1"
  echo b >"$R/deploy/host/apply.sh"
  commit_tag v0.2.0 "$CL1
## [0.2.0] - 2026-11-07

- changed apply"
  ! check v0.2.0
}
case_host_change_with_note() {
  repo
  commit_tag v0.1.0 "$CL1"
  echo b >"$R/deploy/host/apply.sh"
  commit_tag v0.2.0 "## [0.2.0] - 2026-11-07

#### HOST CHANGES

- reinstall apply.sh
$CL1"
  check v0.2.0
}
case_only_tests_changed() {
  repo
  commit_tag v0.1.0 "$CL1"
  echo u >"$R/deploy/host/test/run.sh"
  commit_tag v0.2.0 "## [0.2.0] - 2026-11-07

- test only
$CL1"
  check v0.2.0
}

for c in section first_tag_ok first_tag_needs_host_changes missing_section host_change_without_note host_change_with_note only_tests_changed; do
  if "case_$c"; then pass=$((pass + 1)); echo "ok   $c"; else failures+=("$c"); echo "FAIL $c"; fi
  rm -rf "${R:-/nonexistent}"
done
if ((${#failures[@]})); then echo "${#failures[@]} failed: ${failures[*]}"; exit 1; fi
echo "all $pass cases passed"
```
In `case_host_change_with_note`, the new section is placed above the old content, so the file still contains `## [0.1.0]`. That's fine, because each section is extracted independently.

Run: `chmod 0755 scripts/test/run.sh && scripts/test/run.sh`
Expected: FAIL (the scripts are missing).

- [ ] **Step 2: Implement the scripts**

`scripts/changelog-section.sh`:
```bash
#!/usr/bin/env bash
# Prints the body of a CHANGELOG.md section (without its "## [x]" heading),
# trimmed of surrounding blank lines. Usage: changelog-section.sh vX.Y.Z|X.Y.Z|Unreleased [file]
set -euo pipefail
v=${1:?usage: changelog-section.sh vX.Y.Z|Unreleased [CHANGELOG.md]}
v=${v#v}
file=${2:-CHANGELOG.md}
awk -v v="$v" '
  /^## \[/ { if (insec) exit; if (index($0, "## [" v "]") == 1) { insec = 1; next } }
  insec { lines[++n] = $0 }
  END {
    s = 1; while (s <= n && lines[s] ~ /^[[:space:]]*$/) s++
    e = n; while (e >= s && lines[e] ~ /^[[:space:]]*$/) e--
    for (i = s; i <= e; i++) print lines[i]
  }
' "$file"
```

`scripts/check-release.sh`:
```bash
#!/usr/bin/env bash
# Release gate for tag vX.Y.Z: CHANGELOG.md must have a non-empty section for
# it, with a "Host changes" subsection whenever deploy/host/ (excluding
# test/) changed since the previous v* tag, or exists at the first tag.
set -euo pipefail
tag=${1:?usage: check-release.sh vX.Y.Z}
[[ $tag =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "not a vX.Y.Z tag: $tag" >&2; exit 1; }
here=$(cd "$(dirname "$0")" && pwd)
section=$(git show "$tag:CHANGELOG.md" 2>/dev/null | "$here/changelog-section.sh" "$tag" /dev/stdin || true)
[[ -n $section ]] || { echo "CHANGELOG.md at $tag has no section for ${tag#v}" >&2; exit 1; }
prev=$(git describe --tags --abbrev=0 --match 'v[0-9]*' "$tag^" 2>/dev/null || true)
if [[ -n $prev ]]; then
  changed=$(git diff --name-only "$prev" "$tag" -- deploy/host ':(exclude)deploy/host/test')
else
  changed=$(git ls-tree -r --name-only "$tag" -- deploy/host | grep -v '^deploy/host/test/' || true)
fi
if [[ -n $changed ]] && ! grep -Eqi '^#{2,4}[[:space:]]+host changes[[:space:]]*$' <<<"$section"; then
  echo "deploy/host/ changed since ${prev:-the first commit}, but the ${tag#v} section has no 'Host changes' subsection. Changed:" >&2
  echo "$changed" >&2
  exit 1
fi
echo "release check OK for $tag"
```

`scripts/release.sh`:
```bash
#!/usr/bin/env bash
# Cuts release X.Y.Z locally: moves CHANGELOG "Unreleased" into a dated
# section, bumps frontend/package.json, commits (signed off) and tags vX.Y.Z.
# Never pushes.
set -euo pipefail
die() { echo "release: $*" >&2; exit 1; }
v=${1:?usage: release.sh X.Y.Z}
v=${v#v}
[[ $v =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || die "version must be X.Y.Z, got $v"
cd "$(git rev-parse --show-toplevel)"
[[ -z $(git status --porcelain) ]] || die "working tree is not clean"
! git rev-parse -q --verify "refs/tags/v$v" >/dev/null || die "tag v$v already exists"
grep -q '^## \[Unreleased\]' CHANGELOG.md || die "CHANGELOG.md has no '## [Unreleased]' section"
[[ -n $(scripts/changelog-section.sh Unreleased) ]] || die "the Unreleased section is empty"
awk -v v="$v" -v d="$(date -u +%Y-%m-%d)" '
  /^## \[Unreleased\]/ && !done { print; print ""; print "## [" v "] - " d; done = 1; next }
  { print }
' CHANGELOG.md >CHANGELOG.md.tmp
mv CHANGELOG.md.tmp CHANGELOG.md
(cd frontend && npm version "$v" --no-git-tag-version --allow-same-version >/dev/null)
git add CHANGELOG.md frontend/package.json frontend/package-lock.json
git commit -s -q -m "release: v$v"
git tag -a "v$v" -m "v$v"
if ! scripts/check-release.sh "v$v"; then
  echo "release: check failed; undo with: git tag -d v$v && git reset --hard HEAD~1" >&2
  exit 1
fi
echo "Tagged v$v. Push with: git push origin main v$v"
```

`chmod 0755 scripts/*.sh`.

`CHANGELOG.md`:
```markdown
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
```

`.github/workflows/release.yml`:
```yaml
name: Release

on:
  push:
    tags: ['v*']

permissions:
  contents: read

jobs:
  verify:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0

      - name: Release checks
        run: scripts/check-release.sh "$GITHUB_REF_NAME"

      - uses: actions/setup-go@v5
        with:
          go-version-file: backend/go.mod

      - name: Backend tests
        working-directory: backend
        run: go test ./...

      - uses: actions/setup-node@v4
        with:
          node-version: 20
          cache: npm
          cache-dependency-path: frontend/package-lock.json

      - name: Frontend tests and build
        working-directory: frontend
        run: |
          npm ci
          npm test
          npm run build

      - name: Host script checks
        run: |
          shellcheck deploy/host/apply.sh deploy/host/test/*.sh deploy/host/test/stubs/*
          sudo install -D -m 0755 deploy/host/apply.sh /usr/local/lib/obs-dashboard/apply.sh
          systemd-analyze verify deploy/host/obs-dashboard-apply.path deploy/host/obs-dashboard-apply.service
          deploy/host/test/quadlet-check.sh
          deploy/host/test/run.sh
          scripts/test/run.sh

  image:
    needs: verify
    runs-on: ubuntu-latest
    permissions:
      contents: read
      packages: write
    steps:
      - uses: actions/checkout@v4

      - uses: docker/setup-buildx-action@v3

      - uses: docker/login-action@v3
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}

      - name: Image name
        id: img
        run: echo "name=ghcr.io/${GITHUB_REPOSITORY,,}" >> "$GITHUB_OUTPUT"

      - uses: docker/build-push-action@v6
        with:
          context: .
          platforms: linux/amd64
          push: true
          build-args: VERSION=${{ github.ref_name }}
          tags: ${{ steps.img.outputs.name }}:${{ github.ref_name }}

  release:
    needs: image
    runs-on: ubuntu-latest
    permissions:
      contents: write
    steps:
      - uses: actions/checkout@v4

      - name: Host files and checksums
        run: |
          tar -C deploy/host --exclude=./test -czf "obs-dashboard-host-${GITHUB_REF_NAME}.tar.gz" .
          sha256sum "obs-dashboard-host-${GITHUB_REF_NAME}.tar.gz" > SHA256SUMS
          tar -tzf "obs-dashboard-host-${GITHUB_REF_NAME}.tar.gz"

      - name: Release notes
        run: scripts/changelog-section.sh "$GITHUB_REF_NAME" > notes.md

      - name: Create release
        env:
          GH_TOKEN: ${{ github.token }}
        run: |
          gh release create "$GITHUB_REF_NAME" --title "$GITHUB_REF_NAME" --notes-file notes.md \
            "obs-dashboard-host-${GITHUB_REF_NAME}.tar.gz" SHA256SUMS
```

In `.github/workflows/ci.yml`, append this step to the `host-scripts` job:
```yaml
      - name: Release script tests
        run: scripts/test/run.sh
```

In `Taskfile.yml`, add:
```yaml
  release:
    desc: Cut a release locally (task release VERSION=X.Y.Z); tags but never pushes
    cmds:
      - scripts/release.sh {{.VERSION}}
```

- [ ] **Step 3: Run the tests and linters**

Run: `scripts/test/run.sh && shellcheck scripts/*.sh scripts/test/run.sh`
Expected: `all 7 cases passed`, and shellcheck is clean.

Run: `tar -C deploy/host --exclude=./test -czf /tmp/h.tgz . && tar -tzf /tmp/h.tgz`
Expected: the listing contains `./apply.sh`, `./obs-dashboard.container`, `./obs-dashboard-apply.path`, `./obs-dashboard-apply.service` and `./README.md`, and nothing under `./test`.

- [ ] **Step 4: Commit**
```bash
git add CHANGELOG.md scripts .github/workflows/release.yml .github/workflows/ci.yml Taskfile.yml
git commit -s -m "build: release workflow, changelog and release scripts"
```

---

### Task 9: End-to-end upgrade on a deployed host (user gate)

**Goal:** Prove the whole path on a real host: install `v0.1.0`, upgrade to `v0.1.1` from the UI, then run a forced-failure upgrade that rolls back and stays on `v0.1.1`.

> **USER-ORDERED GATE — NON-SKIPPABLE.** This task was requested by the user in the current conversation. It MUST NOT be closed by walking around it, by declaring it "verified inline", or by substituting a cheaper check. Close only after every item in `acceptanceCriteria` has been re-validated independently, with output captured.

**Files:**
- None in this repo. Verification only. A deployed host is required, which is provisioned outside this repo.

**Acceptance Criteria:**
- [ ] `v0.1.0` is tagged and released. The GitHub release has `obs-dashboard-host-v0.1.0.tar.gz` and `SHA256SUMS`, and `ghcr.io/percona-lab/obs-dashboard:v0.1.0` can be pulled anonymously.
- [ ] On the host, `curl -fsS http://127.0.0.1:4000/healthz` returns `"version":"v0.1.0"`.
- [ ] After `v0.1.1` is released, Settings → Service lists `v0.1.1`. Clicking Upgrade and confirming ends with "Upgraded to v0.1.1". After that, `/healthz` reports `v0.1.1`, and `journalctl -u obs-dashboard-apply` shows `done: running v0.1.1`.
- [ ] A forced failure leaves `/healthz` on `v0.1.1`, and the UI history shows a failed row with the error text. To force the failure, write an `apply-request` for a tag with no image (for example `v0.1.99`, using a real backup name from `/var/lib/obs-dashboard/backups/`), or request an upgrade to a release whose image is broken.

**Verify:** `ssh <host> 'curl -fsS http://127.0.0.1:4000/healthz; journalctl -u obs-dashboard-apply -n 50 --no-pager'` → shows `v0.1.1` and both the success and the rollback runs

**Steps:**
- [ ] **Step 1:** Push the branch, merge it, then run `task release VERSION=0.1.0` and `git push origin main v0.1.0`. Wait for `release.yml` to go green. Make the ghcr package public; this needs a package or org admin.
- [ ] **Step 2:** Install on the host following `deploy/host/README.md`, or with the deployment tooling, and confirm `/healthz`.
- [ ] **Step 3:** Add a changelog entry, then run `task release VERSION=0.1.1` and push the tag. In the UI, run **Check now**, then **Upgrade**, and capture the result.
- [ ] **Step 4:** Force a failure and capture `apply-status` (as shown in the UI history), `/healthz` and the journal.

---

## Self-Review

- **Spec coverage:**
  - §1 version stamp, healthz, changelog, release script and workflow: Tasks 0 and 8.
  - §2 source, config, storage, manager and health: Tasks 1–4.
  - §3 API and UI (Service zone, dot, banner): Tasks 4–6.
  - §4 contract: files in Task 3, host side in Task 7, image in Task 8.
  - §5 host scripts and tests: Task 7.
  - §6 failure table: covered by the tests in Tasks 3, 5 and 7.
  - §7 testing: Tasks 0–9.
  - Non-goals are respected: there's no downgrade UI and no auto-apply.
- **Types:**
  - Go `View` and `ReleaseView` JSON tags match the TypeScript `UpgradesView` and `ReleaseView`: `current, host_files_version, repo, apply_enabled, in_flight, checked_at, releases`, and `version, notes, url, published_at, status, requested_by, requested_at, finished_at, error, host_changes, requires_host, blocked, host_instructions`.
  - `HostUpdateError.RequiresHost` maps to `requires_host` in the 409 body, which `applyUpgrade` parses.
- **Placeholders:** none. The only `vX.Y.Z` strings are in user-facing docs, where they're intentional.
