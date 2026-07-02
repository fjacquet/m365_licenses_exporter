# Dashboards

The demo stack auto-provisions one Grafana dashboard,
`grafana/dashboards/licenses-overview.json` (uid `licenses-overview`, tagged `licenses`,
`licenses_exporter`), via `grafana/provisioning/dashboards/dashboards.yml` (file provider) and
`grafana/provisioning/datasources/datasource.yml` (Prometheus datasource). Bring it up with
`docker compose up` (see [Docker deployment](deployment/docker.md)) and open Grafana at
`http://localhost:3000`.

## Template variables

| Variable | Query | Purpose |
|---|---|---|
| `datasource` | Prometheus datasource picker | Selects which Prometheus to query. |
| `vendor` | `label_values(license_up, vendor)` | Filter to `microsoft` / `vmware` / both (multi-select, "All" default). |
| `instance` | `label_values(license_up{vendor=~"$vendor"}, instance)` | Filter to specific tenants/vCenters. |
| `product` | `label_values(license_seats_total{vendor=~"$vendor"}, product)` | Filter to specific SKUs/license names. |

## Panels

| # | Panel | Type | Query |
|---|---|---|---|
| 1 | Seat utilization % | bar gauge | `100 * license_seats_used / license_seats_total` |
| 2 | Over-allocated licenses | table | `license_seats_used > license_seats_total` |
| 3 | Seats free | stat | `license_seats_total - license_seats_used` |
| 4 | Days to expiration | table (sorted ascending) | `(license_expiration_timestamp_seconds - time()) / 86400` |
| 5 | Expiring < 30d | table | `license_expiration_timestamp_seconds - time() < 30*86400` |
| 6 | Collector health | table | `license_up{vendor=~"$vendor",instance=~"$instance"}` |
| 7 | Last refresh age (s) | stat | `time() - license_collector_last_success_timestamp_seconds` |

Because perpetual licenses **never emit** `license_expiration_timestamp_seconds`
(see [ADR-0005](adr/0005-raw-facts-absent-not-zero-naming-units.md)), they simply do not
appear as rows in the "Days to expiration" or "Expiring < 30d" panels — there is no
`9999`-year row to filter out.

## Alert rules

`deploy/prometheus/license.rules.yml` (loaded by the demo Prometheus) mirrors the dashboard's
raw-facts panels as alerts:

| Alert | Expression | `for` | Severity |
|---|---|---|---|
| `LicenseOverAllocated` | `license_seats_used > license_seats_total` | 15m | warning |
| `LicenseExpiringSoon` | `license_expiration_timestamp_seconds - time() < 30 * 86400` | 1h | warning |
| `LicenseCollectorDown` | `license_up == 0` | 30m | critical |

## Importing elsewhere

Outside the demo stack, import `grafana/dashboards/licenses-overview.json` directly:
**Dashboards → New → Import**, upload the JSON, and select your own Prometheus data source.
No further edits are required — the dashboard's queries only reference the generic
`license_*` metric family (see [ADR-0004](adr/0004-generic-prefix-vendor-label-schema.md)),
so it works unmodified against any exporter instance regardless of which vendors are enabled.
