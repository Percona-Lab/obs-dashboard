# Live server log in Settings — Design

Date: 2026-11-12
Status: approved in brainstorming, pending written-spec review

## Goal

Let a signed-in user read the backend's own log from the dashboard, live,
without SSH access to the host.

The trigger: the Release tab showed "GitHub data may be out of date:
internal error". The real cause is only in the backend log
(`release: refresh failed err=...`), and the user did not know where that log
lives or how to reach it.

## Decisions (made with the user)

- The backend writes every log line to a **file** as well as to standard
  error. Standard error stays, so `journalctl -u obs-dashboard` keeps working.
- The view is **live**: new lines appear while the user watches.
- The view is its own **sub-tab inside Settings**. Settings splits into
  "General" (the Service and Access sections, as today) and "Server log".
  The log fills the page height.
- The user approved the behaviour listed under "The page" and "The backend"
  below as proposed. Items marked *(builder's call)* were not discussed.

## Non-goals

- Text search, a date picker, download, or reading logs older than the two
  files kept.
- Changing the log level from the page.
- Changing the log format, or structured (JSON) logging.
- Reading the host's journal or other containers' logs.

## Anti-goals

- Log lines must never reach a visitor who is not signed in. They must not
  travel on `/api/stream`, which every visitor receives.
- Logging must never block or fail because of the file or a slow viewer.
- No scrubbing is added. The log is trusted not to hold secrets today (tokens
  are not logged); the viewer does not change that, and new code must keep it
  true.

## The backend

### Capturing the log

`slog`'s default handler writes through the standard `log` package. Setting
`log.SetOutput` to a writer that fans out to standard error and to a new log
sink captures every `slog` and `log` line with the format unchanged
(`2026/11/12 14:03:11 WARN release: refresh failed err="..."`). This is set
first thing in `main`, before the first log line.

### The sink (new package, e.g. `backend/internal/logsink`)

One type that is an `io.Writer` and owns:

- **The file.** `obs-dashboard.log` in the same directory as the database
  (`filepath.Dir(cfg.Store.DBPath)`, `/data` in the container, which is
  `/var/lib/obs-dashboard` on the host). No new configuration key.
  The sink starts before config is loaded, so it buffers the first lines in
  memory (at most 1000; older ones are dropped) and writes them once the file
  is opened. Until then the tail is served from that buffer, so early lines
  are visible to a viewer either way *(builder's call)*.
- **Lines.** One entry is one line. A write holding several lines (a panic's
  stack) is split on newlines *(builder's call)*.
- **Rotation.** When the file passes 10 MB it is renamed to
  `obs-dashboard.log.1` (replacing any earlier one) and a new file is
  started. At most two files exist. Written in-house; no new dependency.
- **Subscribers.** Each viewer connection registers a buffered channel of
  lines. A full channel drops the line for that viewer; the write never
  waits.
- **Tail.** Returns the last N lines, reading the current file and, when it
  is short, the previous one.

Tail-then-subscribe happens under the sink's lock, so a viewer gets every
line exactly once: no gap and no duplicate between the history and the live
lines.

If the file cannot be opened or written, the sink reports it once on standard
error and carries on: standard error and live viewers still get every line,
and the tail is empty.

### The endpoint

`GET /api/logs/stream`, in a route group with `authSvc.RequireUser`, the same
rule as upgrades. Only allowlisted accounts can sign in, so a signed-in user
is an allowed one; no further check is needed. The HTTP server sets no write
timeout, so a long-lived stream is not cut (as with `/api/stream`). Server-sent events, built like `streamHandler`
(same headers, 25 s heartbeat comment):

1. an event `reset` carrying the last 500 lines as a JSON array of strings;
2. then one `line` event per new log line, each a JSON string.

A reconnect gets a new `reset`, which replaces what the page holds. That keeps
reconnection simple and correct, at the cost of resending 500 lines.

With sign-in disabled (no `GOOGLE_CLIENT_ID`) there is no signed-in user, so
the log is not reachable from the page. That matches the rest of Settings.

## The page

- **Sub-tabs.** `SettingsPanel` gets "General" and "Server log" sub-tabs.
  General is the default and holds today's content unchanged. The sub-tab
  choice need not survive a reload *(builder's call)*.
- **Connection.** The stream is opened when the Server log sub-tab is shown
  and closed when it is left. No connection exists otherwise.
- **On open.** The last 500 lines appear, scrolled to the bottom. New lines
  are added below.
- **Following.** While the view is at the bottom it stays at the bottom. If
  the user scrolls up, it stops moving, and a "Jump to latest" button
  appears.
- **Pause.** Stops adding new lines to the view. They are kept, and appear on
  resume.
- **Errors and warnings only.** A switch that hides every line whose level is
  not WARN or ERROR, in what is loaded and in what arrives. The level is the
  word after the timestamp; a line without one (a `log.Printf` line, a
  panic's stack) counts as plain and is hidden by the switch
  *(builder's call)*.
- **Appearance.** Monospace, lines shown exactly as written, long lines
  wrap. WARN lines amber, ERROR lines red, the rest plain, using the existing
  theme colours.
- **Limit.** The page keeps at most 5000 lines and drops the oldest.
- **Connection state.** A marker shows "Live", or "Reconnecting" while the
  stream is down. `EventSource` reconnects by itself. On a stream error the
  page also re-checks the session (`loadAuth`); if the user is signed out,
  the existing guard moves them off Settings, which closes the stream
  *(builder's call)*.
- **Signed out.** Settings as a whole is already hidden; nothing to add.

Mockup of the chosen layout:
`.superpowers/brainstorm/3504941-1794494549/content/log-placement.html`
(option B; not committed).

## Left to the builder

- Names of the package, type, component and composable.
- How the tail reads the end of a file efficiently.
- Exact wording, spacing and test ids on the page.

## Tests

- Sink: a write reaches standard error, the file and a subscriber; rotation
  at the size limit keeps exactly two files; tail spans both files; a full
  subscriber does not block a write; an unwritable file does not fail a
  write; lines buffered before the file opens end up in it.
- Endpoint: 401 when signed out; `reset` then `line` events when signed in.
- Page: lines render with level colours; the switch filters; pause holds and
  releases lines; a `reset` replaces the content; the stream closes when the
  sub-tab is left.

## Documentation

`deploy/host/README.md` gains a short note: where the log file is on the
host, and that `journalctl -u obs-dashboard` shows the same lines.
`CHANGELOG.md` gets an "Added" entry.
