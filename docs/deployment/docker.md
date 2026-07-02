# Docker deployment

The image (`Dockerfile`) is a non-root, multi-stage Alpine build: it runs as the
unprivileged `licenses` user (uid `10001`), listens on `9105`, and reads `config.yaml` from
`/etc/licenses_exporter/config.yaml`.

## Standalone container

```bash
docker run -d --name licenses_exporter -p 9105:9105 \
  -e M365_TENANT_ID=... \
  -e M365_CLIENT_ID=... \
  -e M365_CLIENT_SECRET=... \
  -e VCENTER_HOST=https://vcsa01/sdk \
  -e VCENTER_USER=... \
  -e VCENTER_PASSWORD=... \
  -v /path/to/config.yaml:/etc/licenses_exporter/config.yaml:ro \
  ghcr.io/fjacquet/licenses_exporter:latest
```

`config.yaml` is the source of truth for which `${ENV}` references are actually consumed
(`${M365_TENANT_ID}`, `${VCENTER_PASSWORD}`, etc.) — every variable the mounted config
references must exist in the container's environment, or the exporter fails fast at load
with `config references unset environment variable "..."`. Secrets can alternatively be
supplied as files via `passwordFile` (VMware) / `clientSecretFile` (M365) in `config.yaml`,
mounted as read-only volumes instead of passed as env vars.

`/metrics` and `/health` are both served on `9105`; `/health` returns HTTP 200 with
`starting` until the first collection cycle completes for every enabled source, then `ok`.

## One-command demo stack (Compose)

```bash
docker compose up
```

`docker-compose.yml` builds the exporter from the local `Dockerfile` and brings up:

- **`licenses_exporter`** (`:9105`) — built locally, config mounted from `./config.yaml`.
- **`prometheus`** (`:9090`) — scrapes the exporter per `prometheus.yml` and loads the
  alert rules in `deploy/prometheus/license.rules.yml`.
- **`grafana`** (`:3000`, `admin`/`admin` by default) — auto-provisioned with the Prometheus
  datasource and the **Enterprise Licenses — Overview** dashboard
  (`grafana/dashboards/licenses-overview.json`); see [Dashboards](../dashboards.md).

The bundled `config.yaml` ships with placeholder `${M365_*}`/`${VCENTER_*}` env references;
`docker-compose.yml` supplies default literal values for those so the stack starts without
any `.env` file, purely to demonstrate the wiring end-to-end. Override them (shell env or a
`.env` file next to `docker-compose.yml`) with real tenant/vCenter credentials to point the
demo at a live environment.

To run the **published** image instead of building locally:

```bash
VCENTER_PASSWORD='your-monitor-password' docker compose -f docker-compose.ghcr.yml up -d
```

Pin a version with `LICENSES_EXPORTER_TAG` (defaults to `:latest`):

```bash
LICENSES_EXPORTER_TAG=0.2.1 VCENTER_PASSWORD='...' docker compose -f docker-compose.ghcr.yml up -d
```

## Required permissions before first run

### Microsoft 365 — Graph application permission

The M365 collector calls `GET /v1.0/subscribedSkus` as the app registration configured by
`tenantId`/`clientId`/`clientSecret`. That app registration must be granted the Microsoft
Graph **application permission `Organization.Read.All`** (or the broader
`Directory.Read.All`), with **admin consent granted** in Entra ID — application permissions
cannot be self-consented by a non-admin. Without this grant, `Collect` fails with an
authorization error and that tenant's cycle degrades to `license_up{vendor="microsoft",...}=0`
rather than blocking the whole exporter (see [ADR-0002](../adr/0002-prometheus-snapshot-model.md)).

### VMware vSphere — a read-only vCenter role

The VMware collector only needs to log in and read `LicenseManager` state — it never
modifies anything. Create a dedicated vCenter **read-only monitoring account** (do not reuse
an administrator account) and grant it, at minimum, the built-in **Read-only** vSphere role
at the vCenter root (or a custom role granting the `Global.Licenses` read privilege). Each
collection cycle authenticates with this account, performs one license query, and logs out
immediately — no session is held open between cycles (see
[ADR-0007](../adr/0007-token-auth-retry-policy.md)).

## Flags

| Flag | Default | Meaning |
|---|---|---|
| `--config` | `config.yaml` | Path to the config file. |
| `--web.listen-address` | `:9105` | Address the HTTP server (metrics + health) binds to. |
| `--once` | `false` | Run a single collection cycle and exit instead of serving. |
| `--debug` | `false` | Debug-level logging; combined with `--once` it dumps every collected sample (sorted, exposition style) — see `docs/metrics.md`. |
| `--trace` | `false` | Logs repo-owned API responses for live payload validation. Both vendor SDKs are non-injectable, so this **never** enables SDK-level debug output, which would leak the bearer token / session cookie — see [ADR-0007](../adr/0007-token-auth-retry-policy.md). |

Config reload is live: `SIGHUP`, or any write/create to the config file, triggers a
validated hot reload (see [ADR-0008](../adr/0008-config-hot-reload.md)) without a restart or
any interruption to `/metrics`.
