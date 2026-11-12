# Live Server Log in Settings Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A signed-in user reads the backend's own log, live, in a "Server log" sub-tab of Settings.

**Architecture:** A new `logsink` package is the `log` package's output: it writes every line to standard error and to a size-capped file, and fans lines out to subscribers. A signed-in-only server-sent-events endpoint sends the last 500 lines, then each new line. A Vue composable owns the `EventSource`; a panel renders it inside a new Settings sub-tab.

**Tech Stack:** Go standard library only (no new dependency), chi router, Vue 3 + TypeScript, vitest + @vue/test-utils (jsdom).

**Spec:** `docs/superpowers/specs/2026-11-12-server-log-viewer-design.md`

## Global Constraints

- Commit with `git commit -s`. Never add a `Co-Authored-By:` trailer.
- Log format unchanged: capture through `log.SetOutput`, do not replace the `slog` handler.
- Log lines never travel on `/api/stream`; the log endpoint is under `authSvc.RequireUser`.
- A log write never blocks and never returns an error because of the file or a viewer.
- File: `obs-dashboard.log` in `filepath.Dir(cfg.Store.DBPath)`; rotate at 10 MB to `obs-dashboard.log.1`; at most two files.
- Numbers: 500 lines of history, 1000 early lines buffered before the file opens, 256-line channel per viewer, 5000 lines kept in the page, 25 s heartbeat.
- One entry is one line (a multi-line write is split on `\n`).
- No new configuration key, no new Go or npm dependency.

**User decisions (already made):** "Can we start logging to a file at the same time we log to stderr?" (yes, both). "I want a live log." Placement: "B" (own sub-tab inside Settings, log fills the page height). Backend and page behaviour as listed in the spec: "looks good".

## Review Focus

1. A viewer is connected when the file rotates: it keeps receiving every new line.
2. Many goroutines log at once: lines in the file and to viewers are whole, never interleaved; `go test -race` is clean.
3. A viewer disconnects: its subscription is removed, and later writes do not panic or block.
4. A single line far longer than usual (64 KB): it is written, returned by the tail and streamed, not truncated into a broken entry.
5. A log line containing HTML (`<script>alert(1)</script>`): the page shows it as text.

Each has a test in the owning task below (1 to 4 in Task 1, 5 in Task 3).

---

### Task 1: The log sink

**Goal:** Package `logsink`: an `io.Writer` that tees to standard error and a rotating file and serves tail-plus-follow subscriptions.

**Files:**
- Create: `backend/internal/logsink/sink.go`
- Test: `backend/internal/logsink/sink_test.go`

**Interfaces (used by Task 2):**

```go
package logsink

// New returns a sink that copies every write to stderr. Until Open is
// called it keeps the last 1000 lines in memory.
func New(stderr io.Writer) *Sink

// Write implements io.Writer. It always returns len(p), nil.
func (s *Sink) Write(p []byte) (int, error)

// Open starts writing to path (append, create, 0o640), first writing the
// buffered early lines. On failure it writes one line to stderr and the
// sink carries on without a file.
func (s *Sink) Open(path string)

// Subscribe returns the last n lines and a channel of the lines written
// after them, with no gap and no duplicate. cancel removes the
// subscription and is safe to call more than once.
func (s *Sink) Subscribe(n int) (tail []string, lines <-chan string, cancel func())
```

Unexported field `maxSize int64` (default `10 << 20`) so tests can rotate at a small size.

**Acceptance Criteria:**
- [ ] A write reaches stderr, the file and a subscriber, split into one entry per line without the trailing newline.
- [ ] When the file exceeds `maxSize` it becomes `<path>.1` and a new file starts; never more than two files.
- [ ] `Subscribe(n)` tail spans `<path>.1` and `<path>` when the current file holds fewer than n lines.
- [ ] Before `Open`, the tail comes from the early buffer (capped at 1000); after `Open` those lines are in the file.
- [ ] A subscriber whose channel (capacity 256) is full loses lines; `Write` does not wait.
- [ ] `Open` on an unwritable path: one stderr line, later writes still return `len(p), nil` and reach stderr and subscribers; tail is empty.
- [ ] Review Focus 1 to 4 hold.

**Verify:** `cd backend && go test -race ./internal/logsink/` → `ok`

**Steps:**

- [ ] **Step 1: Write the failing tests** in `sink_test.go` (use `t.TempDir()`, a `bytes.Buffer` for stderr):

| Test | Asserts |
|------|---------|
| `TestWriteReachesStderrFileAndSubscriber` | after `Open`, `Write("a\nb\n")`: stderr == `"a\nb\n"`, file == `"a\nb\n"`, channel yields `"a"`, `"b"` |
| `TestEarlyLinesAreBufferedThenWritten` | 3 writes before `Open`: `Subscribe(10)` tail has the 3; after `Open` the file has them in order |
| `TestEarlyBufferIsCapped` | 1005 writes before `Open`: tail of 2000 has 1000 lines, first is line 6 |
| `TestRotationKeepsTwoFiles` | `maxSize = 64`; write 400 bytes in 10-byte lines: dir holds exactly `obs-dashboard.log` and `obs-dashboard.log.1`; current file ≤ 64 + one line |
| `TestTailSpansBothFiles` | after a rotation, `Subscribe(n)` returns the last n lines in order, some from `.1` |
| `TestSubscribeHasNoGapOrDuplicate` | a goroutine writes lines 0..1999 while another subscribes mid-way with n=5000: tail + channel together are a contiguous, duplicate-free run ending at 1999 |
| `TestFullSubscriberDoesNotBlock` | subscribe, never read, write 1000 lines: all writes return within the test; stderr has 1000 lines |
| `TestCancelRemovesSubscriber` | cancel twice, then write: no panic, nothing blocks |
| `TestUnwritableFileDoesNotFailWrites` | `Open(filepath.Join(dir, "missing", "x.log"))`: stderr gains exactly one line mentioning the path; a later write returns `(len, nil)` and reaches a subscriber; tail is empty |
| `TestSubscriberSurvivesRotation` | `maxSize = 64`; subscriber reads every line written across 3 rotations |
| `TestConcurrentWritersKeepLinesWhole` | 8 goroutines × 200 lines of `"g<i>-<j>"`: file has 1600 lines, each matches `^g\d-\d+$` |
| `TestVeryLongLine` | one 64 KB line then `"end"`: `Subscribe(2)` tail is `[<64 KB line>, "end"]` |

- [ ] **Step 2: Run** `cd backend && go test ./internal/logsink/` → FAIL (package has no `Sink`).

- [ ] **Step 3: Implement `sink.go`.** One mutex guards the file, its size, the early buffer and the subscriber set; `Write` does everything under it (stderr write, file write, rotate, non-blocking channel sends with `select`/`default`), so tail-then-subscribe under the same lock gives the no-gap guarantee. Rotation: close, `os.Rename(path, path+".1")`, reopen; on any file error after `Open`, report once on stderr and stop using the file. Tail: read the end of the current file (and `.1` if short) backwards in chunks until n newlines are found; the chunk size is the builder's call.

- [ ] **Step 4: Run** `cd backend && go test -race ./internal/logsink/` → PASS. Then `gofmt -l backend/internal/logsink` → no output, `cd backend && go vet ./internal/logsink/` → clean.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/logsink
git commit -s -m "feat(logs): a log sink that tees to stderr and a capped file and serves a live tail"
```

```json:metadata
{"files": ["backend/internal/logsink/sink.go", "backend/internal/logsink/sink_test.go"], "verifyCommand": "cd backend && go test -race ./internal/logsink/", "acceptanceCriteria": ["write reaches stderr, file and subscriber, one entry per line", "rotation at maxSize keeps exactly two files", "tail spans both files", "early lines buffered (cap 1000) and written on Open", "full subscriber never blocks Write", "unwritable file never fails Write", "Review Focus 1-4 covered by tests"], "modelTier": "standard"}
```

---

### Task 2: Capture the log and stream it

**Goal:** The backend routes its log through the sink and serves `GET /api/logs/stream` to signed-in users.

**Files:**
- Modify: `backend/cmd/obsboard/main.go` (`main`, `run`)
- Modify: `backend/internal/api/server.go` (`NewRouter`)
- Create: `backend/internal/api/logs.go`
- Test: `backend/internal/api/logs_test.go`
- Modify: every `NewRouter(` call in `backend/internal/api/*_test.go` (7 calls) to pass the new argument

**Interfaces:**

```go
// in backend/internal/api/logs.go
// LogSource is what the log stream needs from the sink.
type LogSource interface {
	Subscribe(n int) (tail []string, lines <-chan string, cancel func())
}

func logStreamHandler(src LogSource) http.HandlerFunc
```

`NewRouter` gains a last parameter `logs LogSource`. When non-nil it registers, in its own group with `authSvc.RequireUser`, `r.Get("/api/logs/stream", logStreamHandler(logs))`. Existing test callers pass `nil`.

Wire format (same headers and 25 s `: ping` heartbeat as `streamHandler`):

```
event: reset
data: ["line 1","line 2"]

event: line
data: "line 3"

```

`reset` carries `Subscribe(500)`'s tail as a JSON array (`[]`, never `null`, when empty); each later line is one `line` event holding a JSON string.

In `main.go`: a package-level `var logs = logsink.New(os.Stderr)`; the first statement of `main` is `log.SetOutput(logs)`; in `run`, right after `config.Load` succeeds, `logs.Open(filepath.Join(filepath.Dir(cfg.Store.DBPath), "obs-dashboard.log"))`; pass `logs` to `api.NewRouter`.

**Acceptance Criteria:**
- [ ] Signed out: `GET /api/logs/stream` → 401.
- [ ] Signed in: the response starts with a `reset` event holding the tail, then one `line` event per new line.
- [ ] An empty tail is sent as `data: []`.
- [ ] Client disconnect ends the handler and calls `cancel`.
- [ ] `slog.Info` output keeps today's format and lands in the file.
- [ ] `/api/stream` is unchanged.

**Verify:** `cd backend && go test ./internal/api/ ./cmd/... && go build ./... && go vet ./...` → all `ok`

**Steps:**

- [ ] **Step 1: Write the failing tests** in `logs_test.go`, with a fake `LogSource` (fixed tail, a channel the test feeds, a `cancelled` flag). Reuse the signed-in router helper pattern of `upgrades_routes_test.go` (router + session id).

| Test | Asserts |
|------|---------|
| `TestLogStreamRequiresUser` | no session cookie → status 401 |
| `TestLogStreamSendsResetThenLines` | signed in, tail `["a","b"]`, then feed `c "quoted"`: body contains `event: reset\ndata: ["a","b"]\n\n` then `event: line\ndata: "c \"quoted\""\n\n` |
| `TestLogStreamEmptyTail` | nil tail → `data: []` |
| `TestLogStreamCancelsOnDisconnect` | cancel the request context → handler returns and `cancelled` is true |
| `TestLogStreamAbsentWithoutSource` | router built with `nil` logs, signed in → 404 |

- [ ] **Step 2: Run** `cd backend && go test ./internal/api/` → FAIL (compile: `NewRouter` arity, `LogSource` undefined).

- [ ] **Step 3: Implement** `logs.go`, the `NewRouter` parameter and route group, the `nil` argument in the 7 test callers, and the `main.go` wiring.

- [ ] **Step 4: Run** the Verify command → PASS; `gofmt -l backend` → no output.

- [ ] **Step 5: Check by hand** that the format is unchanged and the file fills: run the backend locally the way the repo's README describes, then compare a stderr line with the last line of `obs-dashboard.log` beside the database: identical, shaped `YYYY/MM/DD HH:MM:SS INFO ...`.

- [ ] **Step 6: Commit**

```bash
git add backend
git commit -s -m "feat(logs): write the server log to a file and stream it to signed-in users"
```

```json:metadata
{"files": ["backend/cmd/obsboard/main.go", "backend/internal/api/server.go", "backend/internal/api/logs.go", "backend/internal/api/logs_test.go"], "verifyCommand": "cd backend && go test ./internal/api/ ./cmd/... && go build ./... && go vet ./...", "acceptanceCriteria": ["401 when signed out", "reset event with tail then line events", "empty tail is []", "disconnect cancels the subscription", "log format unchanged and written to obs-dashboard.log beside the database", "/api/stream unchanged"], "modelTier": "standard"}
```

---

### Task 3: The log panel

**Goal:** A composable that owns the log stream and a panel that renders it with follow, pause and an errors-and-warnings switch.

**Files:**
- Create: `frontend/src/composables/useServerLog.ts`
- Create: `frontend/src/components/ServerLogPanel.vue`
- Test: `frontend/src/composables/useServerLog.test.ts`
- Test: `frontend/src/components/ServerLogPanel.test.ts`

**Interfaces (used by Task 4):**

```ts
// useServerLog.ts
export type LogLevel = 'error' | 'warn' | 'plain'
export interface LogLine { id: number; text: string; level: LogLevel }
export const MAX_LINES = 5000

// 'error' / 'warn' when the word after the "YYYY/MM/DD HH:MM:SS " timestamp
// starts with ERROR / WARN; otherwise 'plain'.
export function levelOf(text: string): LogLevel

export function useServerLog(): {
  lines: Ref<LogLine[]>   // what the page shows, at most MAX_LINES
  live: Ref<boolean>      // true between onopen and onerror
  paused: Ref<boolean>    // while true, new lines are held and added when it turns false
  start: () => void       // opens EventSource('/api/logs/stream')
  stop: () => void        // closes it
}
```

Stream handling: `reset` replaces `lines` (and drops anything held by pause); `line` appends, or is held while paused; over `MAX_LINES` the oldest are dropped. `onerror` sets `live` false and calls `loadAuth()` from `useAuth` (the existing Settings guard then moves a signed-out user away); `EventSource` reconnects by itself. Each instance has its own state (not a module singleton).

`ServerLogPanel.vue` (no props): calls `start()` on mount and `stop()` on unmount. Test ids: `log-state` (text `Live` or `Reconnecting`), `log-pause` (button, text `Pause` / `Resume`), `log-errors-only` (checkbox labelled `Errors and warnings only`), `log-view` (the scrolling box), `log-line` (one per shown line), `log-jump` (button `Jump to latest`, shown only when not at the bottom). Lines: monospace, `whitespace-pre-wrap break-all`, text via `{{ }}` only (never `v-html`); `text-fail` for error, `text-warn` for warn. The box fills the remaining viewport height (for example `h-[calc(100vh-220px)] min-h-[320px] overflow-auto`), styled like the other cards (`rounded-[14px] border border-border bg-bg-card`). Following: after lines change, if the view was at the bottom (within 24 px) scroll to the bottom; a scroll event updates that flag; `log-jump` scrolls to the bottom.

**Acceptance Criteria:**
- [ ] `levelOf` classifies WARN, ERROR, INFO and timestamp-less lines as specified.
- [ ] `reset` replaces content; `line` appends; the list never exceeds 5000.
- [ ] Pause holds lines and releases them on resume.
- [ ] The switch shows only error and warn lines, for loaded and arriving lines.
- [ ] `Live` / `Reconnecting` follow the stream state; a stream error calls `loadAuth`.
- [ ] The stream is closed on unmount.
- [ ] HTML in a line is shown as text (Review Focus 5).

**Verify:** `cd frontend && npx vitest run src/composables/useServerLog.test.ts src/components/ServerLogPanel.test.ts && npx vue-tsc --noEmit` → all pass

**Steps:**

- [ ] **Step 1: Write the failing tests.** Both files start with `// @vitest-environment jsdom` and stub a fake `EventSource` with `vi.stubGlobal` (records the URL, `addEventListener`, `close`, and lets the test fire `open`, `error`, `reset`, `line`). Stub `fetch` for the `loadAuth` call.

`useServerLog.test.ts`:

| Test | Asserts |
|------|---------|
| `levelOf` | `'2026/11/12 14:03:11 WARN x'` → `warn`; `'… ERROR x'` → `error`; `'… INFO x'` → `plain`; `'goroutine 1 [running]:'` → `plain`; `'ERROR at start'` → `plain` |
| `opens the log stream` | after `start()`, the fake was built with `/api/logs/stream` |
| `reset replaces, line appends` | reset `["a","b"]`, line `"c"` → texts `a,b,c`; second reset `["z"]` → `z` |
| `caps at MAX_LINES` | reset with 5000, then 10 lines → length 5000, last text is the 10th |
| `pause holds, resume releases` | paused, 2 lines → length unchanged; unpause → both appended in order |
| `live follows open and error` | open → `live` true; error → false and `fetch` called with `/api/me` |
| `stop closes` | `stop()` → fake `close` called |

`ServerLogPanel.test.ts`:

| Test | Asserts |
|------|---------|
| `renders lines with level colours` | reset with an INFO, a WARN and an ERROR line → 3 `log-line`; the WARN one has class `text-warn`, the ERROR one `text-fail` |
| `errors and warnings only` | tick `log-errors-only` → 2 `log-line`; a new INFO line adds none, a new ERROR line adds one |
| `shows the stream state` | before open `Reconnecting`; after open `Live` |
| `pause button` | click `log-pause` → text `Resume`; a line adds no `log-line`; click again → it appears |
| `shows HTML as text` | line `<script>alert(1)</script>` → `log-line` text equals it; `wrapper.find('script').exists()` is false |
| `closes the stream on unmount` | `wrapper.unmount()` → fake `close` called |

- [ ] **Step 2: Run** the vitest command → FAIL (modules not found).

- [ ] **Step 3: Implement** `useServerLog.ts`, then `ServerLogPanel.vue`.

- [ ] **Step 4: Run** the Verify command → PASS.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/composables/useServerLog.ts frontend/src/composables/useServerLog.test.ts frontend/src/components/ServerLogPanel.vue frontend/src/components/ServerLogPanel.test.ts
git commit -s -m "feat(logs): a live server log panel"
```

```json:metadata
{"files": ["frontend/src/composables/useServerLog.ts", "frontend/src/composables/useServerLog.test.ts", "frontend/src/components/ServerLogPanel.vue", "frontend/src/components/ServerLogPanel.test.ts"], "verifyCommand": "cd frontend && npx vitest run src/composables/useServerLog.test.ts src/components/ServerLogPanel.test.ts && npx vue-tsc --noEmit", "acceptanceCriteria": ["levelOf classifies lines", "reset replaces, line appends, cap 5000", "pause holds and releases", "errors-and-warnings switch filters", "Live/Reconnecting state, loadAuth on error", "stream closed on unmount", "HTML shown as text"], "modelTier": "standard"}
```

---

### Task 4: The Settings sub-tab, documentation, and a look at the real thing

**Goal:** Settings shows "General" and "Server log" sub-tabs, the docs say where the log is, and the feature is seen working in a browser.

**Files:**
- Modify: `frontend/src/components/SettingsPanel.vue`
- Modify: `frontend/src/components/SettingsPanel.test.ts`
- Modify: `deploy/host/README.md`
- Modify: `CHANGELOG.md`

**Details:**

`SettingsPanel.vue`: a local `ref<'general' | 'log'>('general')` (not persisted). Above the content, two buttons with test ids `settings-tab-general` (text `General`) and `settings-tab-log` (text `Server log`), styled like the repo's existing sub-tab controls (see how `PackagesSubTab` / `ContainersSubTab` are switched in their parent), with `aria-selected` on the active one. `general` renders today's content unchanged (`ServiceZone` and the Access section); `log` renders `<ServerLogPanel v-if=...>` so the stream exists only while that sub-tab is shown.

`CHANGELOG.md`, under `## [Unreleased]`:

```markdown
### Added

- Settings has a "Server log" tab that shows the backend's log live, for signed-in users.
- The backend writes its log to `obs-dashboard.log` next to the database, as well as to standard error.

### Host changes

- No action needed. A log file appears at `/var/lib/obs-dashboard/obs-dashboard.log`. It is capped at 10 MB, with one previous file kept as `obs-dashboard.log.1`. `journalctl -u obs-dashboard` still shows the same lines.
```

`deploy/host/README.md`: a short "Logs" note with the same three facts (the Settings tab, the file path on the host, `journalctl -u obs-dashboard`).

**Acceptance Criteria:**
- [ ] Settings opens on General, with the Service and Access sections as before; existing SettingsPanel tests pass unchanged.
- [ ] Clicking `settings-tab-log` shows the log panel and opens exactly one stream; clicking `settings-tab-general` closes it.
- [ ] CHANGELOG and host README carry the notes above.
- [ ] Whole suites pass: backend tests with `-race`, frontend tests, frontend build.
- [ ] Seen in a browser: lines appear live in the sub-tab.

**Verify:** `cd backend && go test -race ./... && cd ../frontend && npx vitest run && npm run build` → all pass

**Steps:**

- [ ] **Step 1: Write the failing tests** in `SettingsPanel.test.ts` (stub `EventSource` as in Task 3):

| Test | Asserts |
|------|---------|
| `opens on General` | `access-zone` exists, `log-view` does not, no `EventSource` built |
| `Server log tab shows the log and opens the stream` | click `settings-tab-log` → `log-view` exists, `access-zone` does not, one `EventSource` on `/api/logs/stream` |
| `leaving the Server log tab closes the stream` | then click `settings-tab-general` → fake `close` called, `access-zone` back |

- [ ] **Step 2: Run** `cd frontend && npx vitest run src/components/SettingsPanel.test.ts` → the three new tests FAIL.

- [ ] **Step 3: Implement** the sub-tabs in `SettingsPanel.vue`.

- [ ] **Step 4: Write** the CHANGELOG entries and the README note.

- [ ] **Step 5: Run** the Verify command → PASS.

- [ ] **Step 6: Look at it.** Run the app locally with sign-in available (the repo's README says how), open Settings → Server log, and check: history loads, a new line appears within a second of the backend logging one (the polling lines are enough), `Errors and warnings only` filters, Pause holds, scrolling up shows `Jump to latest`. If sign-in cannot be set up locally, say so in the hand-back instead of claiming this step.

- [ ] **Step 7: Commit**

```bash
git add frontend/src/components/SettingsPanel.vue frontend/src/components/SettingsPanel.test.ts deploy/host/README.md CHANGELOG.md
git commit -s -m "feat(logs): a Server log tab in Settings"
```

```json:metadata
{"files": ["frontend/src/components/SettingsPanel.vue", "frontend/src/components/SettingsPanel.test.ts", "deploy/host/README.md", "CHANGELOG.md"], "verifyCommand": "cd backend && go test -race ./... && cd ../frontend && npx vitest run && npm run build", "acceptanceCriteria": ["Settings opens on General with existing content and tests unchanged", "Server log sub-tab shows the panel and opens one stream; leaving closes it", "CHANGELOG Added and Host changes entries, host README note", "backend -race tests, frontend tests and build pass", "lines seen arriving live in a browser, or the gap is reported"], "modelTier": "standard"}
```
