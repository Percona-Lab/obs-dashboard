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
# the host-files version installed
OBS_DASHBOARD_HOST_FILES_VERSION=vX.Y.Z
GOOGLE_CLIENT_ID=...
GOOGLE_CLIENT_SECRET=...
OBS_DASHBOARD_ADMINS=someone@percona.com
# plus per-instance OBS credentials, e.g. OBS_<SLUG>_USERNAME / _PASSWORD
```

Env files do not support inline comments: podman keeps everything after the
`=` as part of the value, so put comments on their own line.

`store.db_path` must stay the default `/data/obsboard.db` when using these host
files: `apply.sh` restores `obsboard.db` in `/var/lib/obs-dashboard`.

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
6. If the apply service hits its start limit (`StartLimitBurst=3` per 5
   minutes) it stops starting. Recover with:

   ```sh
   systemctl reset-failed obs-dashboard-apply.service obs-dashboard-apply.path && systemctl restart obs-dashboard-apply.path
   ```

## Logs

- Signed-in users can watch the backend's log live in Settings, on the "Server log" tab.
- The backend also writes it to `/var/lib/obs-dashboard/obs-dashboard.log`, capped at 10 MB with one previous file kept as `obs-dashboard.log.1`.
- `journalctl -u obs-dashboard` shows the same lines.

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
