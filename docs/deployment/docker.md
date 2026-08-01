# Docker deployment

Both images are non-root Alpine builds running as the unprivileged `licenses`
user (uid `10001`), listening on `9105` and reading `config.yaml` from
`/etc/m365_licenses_exporter/config.yaml`. `Dockerfile` is the multi-stage build
from source; `Dockerfile.goreleaser` is the release image published to GHCR,
which copies the prebuilt binary. Published images before the ADR-0011 release
were `gcr.io/distroless/static:nonroot` at uid `65532`.

## Standalone container

```bash
docker run -d --name m365_licenses_exporter -p 9105:9105 \
  -e M365_TENANT_ID=... \
  -e M365_CLIENT_ID=... \
  -e M365_CLIENT_SECRET=... \
  -v /path/to/config.yaml:/etc/m365_licenses_exporter/config.yaml:ro \
  ghcr.io/fjacquet/m365_licenses_exporter:latest
```

`config.yaml` is the source of truth for which `${ENV}` references are actually consumed
(`${M365_TENANT_ID}`, `${M365_CLIENT_SECRET}`, etc.) — every variable the mounted config
references must exist in the container's environment, or the exporter fails fast at load
with `config references unset environment variable "..."`. Secrets can alternatively be
supplied as a file via `clientSecretFile` in `config.yaml`, mounted as a read-only volume
instead of passed as an env var.

Four routes are served on `9105`:

| Path | Status | Body |
|---|---|---|
| `/metrics` | 200 | Prometheus exposition |
| `/health` | always 200 | `starting` until the first collection cycle completes for every enabled source, then `ok` |
| `/livez` | always 200 | empty |
| `/readyz` | always 200 | empty |

Point Kubernetes probes and container healthchecks at `/livez` and `/readyz` —
never at `/metrics`, which renders the whole exposition per probe tick and can
block behind a slow collection cycle. Both Dockerfiles ship a `HEALTHCHECK`
against `http://127.0.0.1:9105/livez` (`127.0.0.1`, not `localhost`: busybox
`wget` tries `::1` first and the exporter binds IPv4 only), and both compose
files carry the matching `healthcheck:`.

## One-command demo stack (Compose)

```bash
docker compose up
```

`docker-compose.yml` builds the exporter from the local `Dockerfile` and brings up:

- **`m365_licenses_exporter`** (`:9105`) — built locally, config mounted from `./config.yaml`.
- **`prometheus`** (`:9090`) — scrapes the exporter per `prometheus.yml` and loads the
  alert rules in `deploy/prometheus/license.rules.yml`.
- **`grafana`** (`:3000`, `admin`/`admin` by default) — auto-provisioned with the Prometheus
  datasource and the **Enterprise Licenses — Overview** dashboard
  (`grafana/dashboards/licenses-overview.json`); see [Dashboards](../dashboards.md).

The bundled `config.yaml` ships with placeholder `${M365_*}` env references;
`docker-compose.yml` supplies default literal values for those so the stack starts without
any `.env` file, purely to demonstrate the wiring end-to-end. Override them (shell env or a
`.env` file next to `docker-compose.yml`) with real tenant credentials to point the demo at a
live environment.

To run the **published** image instead of building locally:

```bash
docker compose -f docker-compose.ghcr.yml up -d
```

Pin a version with `M365_LICENSES_EXPORTER_TAG` (defaults to `:latest`):

```bash
M365_LICENSES_EXPORTER_TAG=0.2.1 docker compose -f docker-compose.ghcr.yml up -d
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

## Flags

| Flag | Default | Meaning |
|---|---|---|
| `--config` | `config.yaml` | Path to the config file. |
| `--web.listen-address` | `:9105` | Address the HTTP server (metrics + health) binds to. |
| `--once` | `false` | Run a single collection cycle and exit instead of serving. |
| `--debug` | `false` | Debug-level logging; combined with `--once` it dumps every collected sample (sorted, exposition style) — see `docs/metrics.md`. |
| `--trace` | `false` | Logs repo-owned API responses for live payload validation. The Graph SDK is non-injectable, so this **never** enables SDK-level debug output, which would leak the bearer token — see [ADR-0007](../adr/0007-token-auth-retry-policy.md). |

Config reload is live: `SIGHUP`, or any write/create to the config file, triggers a
validated hot reload (see [ADR-0008](../adr/0008-config-hot-reload.md)) without a restart or
any interruption to `/metrics`.
