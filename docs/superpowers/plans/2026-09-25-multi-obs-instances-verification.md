# Task 15: End-to-end verification — evidence log

Run performed 2026-09-25 against real openSUSE OBS and real "labs" OBS
(`obs.pg.labs.percona.com`), from the worktree
`.claude/worktrees/multi-obs-instances` (branch `feat/multi-obs-instances`).

No docker compose was used, per the environment context note. The backend
was built with `go build ./cmd/obsboard` and run directly with `go run`'s
output binary, against **copies** of `data/obsboard.db` in the scratch
directory, on port 4100, with `FRONTEND_DIR` pointed at a `npm run build`
of the worktree's frontend and `idle.enabled=false`. The original
`data/obsboard.db` was only ever copied, never opened. No docker
containers or the user's running stack were touched. All generated
configs/DB copies/logs live under the scratch directory
(`/tmp/claude-1000/.../scratchpad/task15`) and are not part of this
commit.

Credentials were sourced only via environment variables from
`/home/rdias/Work/percona-obs-dashboard/.env` (openSUSE) and a generated
`labsenv.sh` reading `/home/rdias/Work/percona-obs-dashboard/.cred`
(labs). No credential value appears anywhere below or in any committed
file.

## Acceptance criteria

### 1. Single-instance regression (legacy env config, DB copy of the pre-migration DB)

**PASS**

Startup log (`grep -E "stripped root|listening"` on the run log):

```
2026/09/25 16:44:23 INFO store: stripped root from project names table=packages rows=590
2026/09/25 16:44:23 INFO store: stripped root from project names table=events rows=1228
2026/09/25 16:44:23 INFO store: stripped root from project names table=target_state_durations rows=25143
2026/09/25 16:44:23 INFO store: stripped root from project names table=cve_scans rows=116
2026/09/25 16:44:23 INFO listening addr=:4100
```

`curl -s localhost:4100/api/instances`:

```json
[{"name":"openSUSE","slug":"opensuse","root":"isv:percona","web_url":"https://build.opensuse.org","download_url":"https://download.opensuse.org/repositories","registry":"registry.opensuse.org","health":{"ok":true,...,"mq_connected":true}}]
```

One instance, `slug=opensuse`, `root=isv:percona` — matches acceptance
criterion 2 as well.

`curl -s "localhost:4100/api/products/ppg/staging/_/packages"` returned
root-free package/project names, e.g. `"project":"common:containers:ubi8","name":"createrepo_c"` (no `isv:percona:` prefix
anywhere in the payload).

Frontend gating for multi-instance UI (`frontend/src/composables/useInstances.ts:30`,
`const multi = computed(() => instances.value.length > 1)`) is driven
directly off `/api/instances`'s length. With one instance returned, this
is `false`, so `InstanceBadge`, the by-instance chips/card and per-instance
metrics all stay hidden — confirmed by reading the component logic
(`InstanceBadge.vue`, `PackageCard.vue`, `MetricsPanel.vue`), since no
browser was available to see it rendered (see "Manual UI checks" below).

Link resolution: constructed a `project/show` and `package/show` URL for
a real, still-existing package (`isv:percona:ppg:staging:14/percona-postgis`)
using the same `Instance.ToInstance`/`PackageURL`/`DownloadURL` logic
(`backend/internal/obs/instance.go:138-152`) and confirmed both resolve:

```
curl -sI https://build.opensuse.org/package/show/isv:percona:ppg:staging:14/percona-postgis
HTTP/2 200
curl -s -o /dev/null -w '%{http_code}\n' https://download.opensuse.org/repositories/isv:/percona:/ppg:/staging:/14/
200
```

Note: one project referenced in the stale July DB snapshot
(`isv:percona:common:containers:ubi8`) no longer exists on the openSUSE
OBS API (`unknown_project`, confirmed via authenticated
`/source/isv:percona:common:containers:ubi8`) — this is expected drift in
test data since the DB snapshot (2026-07-07), not a link-construction bug;
the same package does exist, and correctly builds, on the labs instance
(see criterion 3). The URL-building logic itself is verified correct via
the `percona-postgis` check above.

### 2. `/api/instances` for legacy config

**PASS** — see the JSON above: one instance,
`{"slug":"opensuse","root":"isv:percona",...}`.

### 3. Two-instance run (openSUSE + labs)

**PASS**

Config (`obs_instances` block, generated only into the scratch dir,
0600 permissions, credentials from `OBS_OPENSUSE_*` / `OBS_LABS_*` env
vars and the labs `mq.url` built from env at run time — never written to
any committed file):

- openSUSE: `root: isv:percona`, `api_url: https://api.opensuse.org`,
  `web_url: https://build.opensuse.org`,
  `download_url: https://download.opensuse.org/repositories`,
  `registry: registry.opensuse.org`, `mq.exchange: pubsub`,
  `mq.routing_prefix: opensuse.obs`.
- labs: `root: isv:percona`, `api_url: https://obs.pg.labs.percona.com`,
  `web_url: https://obs.pg.labs.percona.com`,
  `download_url: https://download.obs.pg.labs.percona.com`,
  `registry: registry.obs.pg.labs.percona.com` (placeholder — see caveat
  below), `mq.exchange: obs.events`, `mq.routing_prefix: percona.obs`
  (values supplied by the user mid-run; see "Labs MQ" section below for
  how they were obtained and what happened when used).

`curl -s localhost:4100/api/instances` (after ~15s uptime):

```json
[
  {"name":"openSUSE","slug":"opensuse","root":"isv:percona", ..., "health":{"ok":true,"consecutive_failures":0,"mq_connected":true}},
  {"name":"labs","slug":"labs","root":"isv:percona", ..., "health":{"ok":true,"consecutive_failures":0,"mq_connected":false}}
]
```

Both instances present, both healthy over the API (labs `mq_connected`
is `false` — see MQ caveat below, this is independent of API health).

**Merged package with targets from both instances, with badges**:
`GET /api/products/ppg/staging/14/packages`, package
`ppg:common:deps/sfcgal` (this is the verified overlap package from the
context notes):

```json
{
  "project": "ppg:common:deps", "name": "sfcgal", "rollup_state": "blocked",
  "targets": [
    {"repo":"openSUSE_Tumbleweed","arch":"x86_64","instance":"opensuse","state":"succeeded"},
    {"repo":"openSUSE_Leap_16","arch":"x86_64","instance":"opensuse","state":"succeeded"},
    {"repo":"RockyLinux_9.6","arch":"x86_64","instance":"opensuse","state":"succeeded"},
    ... (openSUSE: Debian_12/13, Leap_16, Tumbleweed, RockyLinux 8/9/9.6/10, x86_64+aarch64) ...
    {"repo":"UBI_9","arch":"x86_64","instance":"labs","state":"blocked","details":"boost-devel, ..."},
    {"repo":"UBI_9","arch":"aarch64","instance":"labs","state":"blocked","details":"..."},
    {"repo":"UBI_8","arch":"x86_64","instance":"labs","state":"succeeded"},
    {"repo":"UBI_8","arch":"aarch64","instance":"labs","state":"succeeded"}
  ]
}
```

One package, one card, targets tagged with both `instance: "opensuse"`
and `instance: "labs"` — this is the JSON the frontend's `PackageCard.vue`
/ `InstanceBadge.vue` render badges and links from (`instanceFor`,
`obsLinks`, `targetDimmed` all key off `target.instance`).

Beyond the single verified-overlap package, the real fleet discovery
surfaced many more packages carrying `instance: "labs"` (454 packages
total in the two-instance DB copy — e.g. all of `ppg:common:deps:*`,
`ppg:devel:18/19:*`, most of `ppg:staging:14-19:*`, and
`common:containers:ubi8`/`common:containers:ubi9`, which build
exclusively on labs and do not exist on openSUSE — confirmed via
authenticated `unknown_project` on `api.opensuse.org` for that project).
This is a wider real overlap than the single verified package quoted in
the task context, and is expected: labs currently builds the UBI/RHEL
targets for most PPG products.

**Filter dimming**: `PackageCard.vue:195-197`
(`targetDimmed`) sets a target's opacity to `0.45` when
`activeInstances` is non-empty and does not include that target's
instance; the card body's own opacity likewise reflects state-spotlight
filtering. This is driven purely off `target.instance`, which the API
payload above populates correctly, so filtering by one instance will dim
the other's targets as designed. (Visual confirmation left for the user —
no browser available; see "Manual UI checks".)

**"OBS ↗" one link per instance**: `PackageCard.vue:225-230` renders one
`<a>` per entry in `obsLinks`, labelling each with the instance name
(`${link.name} ↗`) when `obsLinks.length > 1`, and falls back to a
single generic `OBS ↗` otherwise — matches the single-instance run above
(which showed a single generic label) and the two-instance data (two
distinct instances on `sfcgal`'s targets, so `obsLinks.length === 2`).

**`/api/metrics` `by_instance`**: two entries —

```json
[
  {"name":"openSUSE","slug":"opensuse","total":312,"health":{"ok":true,"mq_connected":true}},
  {"name":"labs","slug":"labs","total":60,"health":{"ok":true,"mq_connected":false}}
]
```

`/api/overview` `by_instance` also has two entries:

```json
[
  {"instance":"labs","ok":1332,"failing":0,"building":47,"blocked":221},
  {"instance":"opensuse","ok":7733,"failing":12,"building":1,"blocked":0}
]
```

### 4. Outage drill (wrong labs password)

**PASS**

Restarted the two-instance backend with the labs process's
`OBS_LABS_PASSWORD` deliberately wrong (suffix appended, only in that
process's own environment — the correct password on disk was untouched).
Within ~9 seconds (well under the 60–90s budget) `/api/instances` showed:

```json
{"name":"labs", ..., "health":{"ok":false,"last_error":"OBS /build/isv:percona:ppg:staging:19/_result: 401 Unauthorized — <status code=\"authentication_required\">...","consecutive_failures":33,"mq_connected":false}}
```

The `sfcgal` package's labs targets kept their last known state
(re-fetched from `/api/products/ppg/staging/14/packages` during the
outage):

```json
{"repo":"UBI_9","arch":"x86_64","instance":"labs","state":"blocked", ...}
{"repo":"UBI_9","arch":"aarch64","instance":"labs","state":"blocked", ...}
{"repo":"UBI_8","arch":"x86_64","instance":"labs","state":"succeeded"}
{"repo":"UBI_8","arch":"aarch64","instance":"labs","state":"succeeded"}
```

— identical to the pre-outage state; the openSUSE targets were
unaffected throughout.

Restarted again with the correct password: within the same startup
window, `/api/instances` showed labs `health.ok:true,
"consecutive_failures":0` again — recovery confirmed. (`mq_connected`
stayed `false` across both runs — that's the separate MQ permission
issue below, not the password.)

InstanceBadge amber styling (`InstanceBadge.vue:17`,
`const stale = computed(() => !inst.value.health.ok)`) is driven
directly by this same `health.ok` field, so the amber/clear behavior
described in the criterion follows directly from the API evidence above.
(Visual confirmation left for the user; see "Manual UI checks".)

### 5. Startup-discovery-failure drill (labs unreachable from process start) — Critical bug regression check

**PASS**

Started a *fresh* backend process against a DB copy that already had 454
packages carrying labs targets (including packages that exist **only**
on labs, e.g. `common:containers:ubi8/createrepo_c`, confirmed
`unknown_project` on openSUSE), with the labs password wrong from time
zero (never successfully authenticated even once in this process's
lifetime). After ~50s of discovery/poll activity (package count
unchanged: 699 before and after; grep for `delet|removing` in this run's
log: 0 matches, vs. dozens of legitimate deletions of truly-gone
openSUSE projects seen in the normal two-instance run), the labs-only
package was still present with its last good state:

```
sqlite3 obsboard-two-startupfail.db "select project,name,rollup_state from packages where project='common:containers:ubi8' and name='createrepo_c';"
common:containers:ubi8|createrepo_c|succeeded
```

`/api/instances` correctly reported labs `health.ok:false` throughout
(consecutive 401s), confirming this was a genuine "labs never reachable"
scenario, not a fluke — and confirming the carry-forward-targets fix
(commit `826f024 fix(obs): carry forward targets of instances down at
startup`) holds: labs-only packages are not deleted when labs fails to
discover at startup.

## Labs MQ: exchange, routing prefix, and outcome (updated 2026-09-26 — see "Attempt 3" below for the current result)

The context file asked to discover the labs exchange/routing prefix with
a throwaway Go program (passive-declaring `pubsub` then `amq.topic` on
the default vhost). That attempt failed before any exchange check ran:// vhost
`/` gave `403 no access to this vhost`; a vhost named `obs` connected
successfully, but both `pubsub` and `amq.topic` passive-declares then
failed with `ACCESS_REFUSED` (permission, not "not found").

Mid-run, the user supplied the actual labs MQ settings directly (this
was not guessed): host `obs.pg.labs.percona.com:5671` (AMQPS/TLS),
vhost `obs`, username `<labs-mq-username>`, **exchange `obs.events`** (topic,
durable), **routing prefix `percona.obs`** (routing keys shaped
`percona.obs.<event>`). These were plugged into the config
(`mq.exchange: obs.events`, `mq.routing_prefix: percona.obs`) for every
run above.

**Attempt 1 (original labs credentials): `ACCESS_REFUSED`.** Every run's
log up to this point showed:

```
WARN mq: disconnected, reconnecting err="exchange declare: Exception (403) Reason: \"ACCESS_REFUSED - configure access to exchange 'obs.events' in vhost 'obs' refused for user '<labs-mq-username>'\"" backoff=...
```

The backend's own consumer code (`backend/internal/mq/consumer.go:109`)
already calls `ExchangeDeclarePassive` (not an active/non-passive
declare) before binding. At the time, this looked like RabbitMQ gating
even a *passive* `exchange.declare` on "configure" rights — **this
statement is now known to be wrong; see "Attempt 3" below.** The real
cause was a broken broker package build, not an ACL requirement: a
passive declare with read permission alone works fine once the broker
is sound.

**Attempt 2, after the user reported fixing the account's permissions**
(granting configure/write/read on its own server-named queues and read
on `obs.events`): restarted the two-instance backend fresh. Result:
**identical `ACCESS_REFUSED` on the exchange declare**, unchanged from
attempt 1, and consistent across multiple restarts/backoff cycles
(`backoff=1s,2s,4s,8s,16s,30s,30s,...`). `/api/instances` still showed
labs `health.ok:true` (API path unaffected) and `mq_connected:false`.

To isolate whether the *passive declare itself* was the blocker (RabbitMQ
requires "configure" for `exchange.declare` regardless of the `passive`
flag — a widely-reported RabbitMQ ACL quirk), a second throwaway probe
skipped the exchange declare entirely and went straight to
`QueueDeclare` (exclusive, auto-delete, server-named) → `QueueBind(key="#",
exchange="obs.events")` → `Consume`, exactly the minimal permission set
the user described ("consumer-oriented": own queues +
read on `obs.events`). `QueueDeclare` succeeded, but `QueueBind` then
failed with a *different*, non-permission error, reproducible on 3/3
tries:

```
BIND_ERR: Exception (541) Reason: "INTERNAL_ERROR"
```

**Result at the time (attempts 1–2): FAIL for the MQ leg, on both
attempts, with two distinct broker-side errors** — `ACCESS_REFUSED`
(believed to be a configure right missing on `exchange.declare`, even
passive) when going through the backend's own code path, and
`INTERNAL_ERROR` (541, a RabbitMQ broker-side error, not an ACL
rejection) when binding directly without declaring. This was not worked
around; per the controller's instruction the verification continued
with API polling only for labs. labs `health.ok` correctly went `true`
(API path healthy) while `mq_connected` stayed `false` throughout every
two-instance run at that time.

### Attempt 3 (2026-09-26, after the broker fix)

The labs RabbitMQ admin identified and fixed the root cause: the broker
package `rabbitmq-server 4.1.5-160000.2.1` was broken — `rabbit_channel`
called a 4.2-only module, so **every** client's `queue.bind` crashed the
channel (this produced the `INTERNAL_ERROR (541)` seen in attempt 2),
and the same broken build produced the `ACCESS_REFUSED` seen on the
passive exchange declare in attempts 1–2. Neither error was actually a
permissions/ACL problem. The admin downgraded and pinned the package to
`rabbitmq-server 4.1.5-160000.1.1`. **The earlier statement above that
"RabbitMQ requires configure rights for a passive `exchange.declare`,
even passive" is corrected: that was never true — it was this broken
broker build.** A passive declare of `obs.events` now succeeds with the
read permission alone, no "configure" grant needed.

**Update 2026-11-19: the paragraph above no longer holds.** The labs
broker was upgraded to `rabbitmq-server-4.1.5-160000.3.1`, which
includes the fix for CVE-2026-57221: passive queue and exchange
declarations are now authorized, so a passive `exchange.declare` needs
"configure" on the exchange. The consumer account keeps read only on
`obs.events`, so the consumer no longer calls `ExchangeDeclarePassive`
at all; it declares its queue and binds, and the bind fails with a 404
if the exchange is missing. References in this document to
`consumer.go:109` and to "no application-side workaround" describe the
code as it was on 2026-09-26.

Re-verification: built the frontend/backend fresh from the worktree,
regenerated the scratch two-instance config (same vhost `obs`, exchange
`obs.events`, routing prefix `percona.obs`, credentials only via env
vars/scratch files, never committed), took a fresh copy of
`data/obsboard.db`, and ran the backend on port 4100 in the background.

Startup log (both instances) — `mq: connected` for labs, no
`ACCESS_REFUSED`/`INTERNAL_ERROR`/reconnect lines at all in the run:

```
2026/09/26 08:07:07 INFO mq: connected exchange=pubsub instance=opensuse
2026/09/26 08:07:07 INFO mq: connected exchange=obs.events instance=labs
```

Watched for 5.5 minutes (`/api/instances` polled every ~60s, plus a
continuous grep of the log for `mq: disconnected|reconnecting|WARN|ERROR`):
zero reconnects, zero warnings/errors, and `/api/instances` for labs
stayed `"mq_connected": true` and `"health": {"ok": true, ...}` from the
first poll through the last one at t+5m31s (well past the 2-minute
`mqDownGrace` window), e.g. at t+5m31s:

```json
{
  "name": "labs",
  "slug": "labs",
  "health": {
    "ok": true,
    "last_success": "2026-09-26T08:12:19.509483559+01:00",
    "consecutive_failures": 0,
    "mq_connected": true
  }
}
```

**Evidence of message consumption**: none observed in this ~5.5-minute
window. The `events` table (instance='labs') and the `packages` table
(`updated_at`) both show zero rows changed after the connect timestamp,
checked directly against the scratch DB copy (not the original). This
is consistent with — but does not prove — an absence of matching build
activity on labs during the window; it is not proof the consumer path
works end-to-end, only that connect + bind (all three routing keys:
`percona.obs.package.#`, `percona.obs.repo.published`,
`percona.obs.project.#`) succeeded and stayed stable. The codebase has
no log-level flag/env var to force debug output (checked
`backend/cmd/obsboard/main.go` and grepped for `LOG_LEVEL`/`slog.HandlerOptions`
— none exists, and none was added, per the "no application-code
changes" rule), so the debug-level "mq: received raw message" /
"mq: unparseable message" lines in `consumer.go` could not be forced on
without a code change; this is recorded as the limit of what is
observable here, not a fix.

**Net result: PASS for the MQ leg** — labs `mq_connected` is `true` and
stable, well past the 2-minute grace window, with a clean connect and
no reconnect loop. Message-consumption evidence remains unconfirmed by
direct observation in this run (no qualifying MQ traffic was seen), but
the earlier `FAIL` was caused entirely by the broken broker build, now
fixed and pinned upstream; the acceptance criterion is flipped to PASS
on connectivity, with the consumption caveat above recorded rather than
asserted as proven.

**Note on routing-key coverage (non-JSON payloads)**: the dashboard's
consumer binds only three routing keys per instance —
`<prefix>.package.#`, `<prefix>.repo.published`, and
`<prefix>.project.#` (`backend/internal/mq/consumer.go:118-126`). It does
**not** bind `<prefix>.metrics` (which OBS publishes as InfluxDB line
protocol, not JSON) nor `<prefix>.repo.build_started` /
`<prefix>.repo.publish_state`. Confirmed in `consumer.go`: `handle()`
does `json.Unmarshal(msg.Body, &m)` first and, on failure, logs
`slog.Debug("mq: unparseable message", "err", err)` and returns
(`consumer.go:157-161`) — so the consumer already tolerates/drops
unparseable (non-JSON) payloads safely at debug level rather than
crashing or erroring loudly. A future `percona.obs.#` (or any wider)
binding would receive the non-JSON `metrics` messages and must keep
relying on that same drop-on-unparseable behavior (or add explicit
line-protocol handling) rather than assuming every message on the
exchange is JSON.

**Impact this has on the rest of the dashboard**: labs data still
appears and updates via the polling/worker path (confirmed in the
original run — `sfcgal`'s labs targets updated between runs), and as of
Attempt 3 also has a working low-latency MQ push channel (connectivity
confirmed; actual event-driven updates unconfirmed in this window, see
above). The earlier note that `health.ok` would flip `false` after the
2-minute MQ-down grace period no longer applies now that the broker is
fixed and pinned — this was **not** an application-side bug, and no
application-side workaround (e.g. skipping `ExchangeDeclarePassive`) was
needed or made.

## Registry placeholder caveat

The labs registry was configured as `registry.obs.pg.labs.percona.com`,
a **placeholder** per the context notes — the real labs container
registry host is unknown/unverified. Any CVE-scan or registry links the
dashboard builds for labs container images
(`Instance.ImageBase`, `backend/internal/obs/instance.go:154-156`) will
use this placeholder and are **not confirmed to resolve**. This was not
tested against a real registry endpoint and should be treated as
unverified until the real labs registry hostname is confirmed and the
config updated.

## Manual UI checks for the user

No browser was available in this environment, so the following were
verified only via the JSON API and by reading the corresponding Vue
component logic, not by looking at the rendered page. To confirm
visually:

1. **Single-instance run**: start the backend with the legacy env vars
   only (`OBS_USERNAME`/`OBS_PASSWORD`/`OBS_BASE_URL`/`MQ_URL`, no
   `obs_instances`), open the dashboard, and confirm the Overview,
   Builds and Artifacts tabs show no instance badges/chips/"By instance"
   card anywhere, and that package/log/download links open real
   `build.opensuse.org`/`download.opensuse.org` pages.
2. **Two-instance badges**: with both instances configured, open the
   Builds tab, find `ppg:common:deps` → `sfcgal` (or any `ppg:staging:*`
   package, since labs targets are broadly present), and confirm the
   card shows a small badge/chip per instance next to (or on) its
   targets, and that the "OBS ↗" control expands into two links, one per
   instance, each opening the correct instance's OBS project/package
   page.
3. **Filter dimming**: use the instance filter control (top of Builds/
   Artifacts) to select only one instance, and confirm the other
   instance's targets/badges visibly dim (not disappear) on mixed
   packages like `sfcgal`, while single-instance packages fully dim only
   if their instance isn't selected.
4. **Outage amber badge**: with the labs password intentionally wrong
   (as scripted above) and the dashboard open, wait ~60–90s and confirm
   the labs badge turns amber with a "stale" label/tooltip, while
   `sfcgal`'s UBI targets remain visible with their last known
   state (not blanked or removed); restore the password and confirm the
   badge clears back to normal within the same window.
5. **Metrics/Overview by-instance panels**: open the Overview and
   Metrics tabs and confirm each renders two rows/sections (one per
   instance) matching the `by_instance` JSON quoted above.

## Process hygiene

All backend processes started for this verification were stopped before
finishing (`kill` + confirmed via `ps -p <pid>` → no such process, for
each of: single-instance run, two-instance run, outage run, recovery
run, startup-discovery-failure run, and the Attempt 3 MQ re-check run on
2026-09-26). No docker containers were used or touched. The scratch DB
copies, generated config, and the throwaway MQ discovery program live
only under the scratch directory and are not part of this commit.
