# m365_licenses_exporter

[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

A **Microsoft 365** license exporter for the Prometheus/Grafana stack, built on
[`github.com/fjacquet/licenses-exporter-core`](https://github.com/fjacquet/licenses-exporter-core).
It periodically polls the Microsoft Graph API (`subscribedSkus`) for one or more tenants and
normalizes seat/entitlement data into the shared `license_` Prometheus schema, exposed via
**both** a Prometheus `/metrics` endpoint and an OTLP metric push, fed from a single shared
snapshot.

Part of the `licenses_exporter` family; shares the `license_` schema via
`licenses-exporter-core` — see [ADR-0010](docs/adr/0010-consume-licenses-exporter-core.md).

## Metrics

One `license_` prefix shared across the family; vendors are distinguished by labels, not by
metric name:

| Metric | Labels | Notes |
|---|---|---|
| `license_seats_total` | `vendor,product,unit,instance` | Omitted for unlimited/perpetual entitlements — never a `0`/`9999` sentinel. |
| `license_seats_used` | `vendor,product,unit,instance` | Raw fact, always emitted when known. |
| `license_expiration_timestamp_seconds` | `vendor,product,instance` | Omitted entirely for perpetual licenses. |
| `license_up` | `vendor,instance` | `1`/`0` per source's last collection cycle. |
| `license_collector_last_success_timestamp_seconds` | `vendor,instance` | Unix timestamp of the last successful collection. |
| `license_scrape_duration_seconds` | `vendor,instance` | Time spent collecting that source. |
| `license_build_info` | `version,goversion` | Constant `1`; exporter build metadata. |

No exporter-computed `days_to_expiration` or compliance verdict — derive those in PromQL /
alert rules from the raw facts above. An unparseable value yields an absent sample, never a
fake `0`. At cold start only `license_build_info` is emitted; per-target series appear once
each source's first collection cycle resolves.

## Quick start

```bash
make cli
./bin/m365_licenses_exporter --config config.yaml
# metrics: http://localhost:9105/metrics   health: http://localhost:9105/health
# probes:  http://localhost:9105/livez     http://localhost:9105/readyz  (always 200)
```

Useful flags: `--once --debug` runs a single collection cycle and dumps every collected
sample (sorted, exposition style) instead of serving; `--trace` logs repo-owned API response
bodies for live payload validation (never SDK debug modes, which would leak the bearer
token).

## Configuration

The M365 collector is toggled in `config.yaml` (`m365.enabled`), not via environment
variables. Secrets are referenced as `${ENV}` placeholders inside `config.yaml` (or via
`clientSecretFile` for file-based secrets); a `.env` file is a convenience for local `${ENV}`
expansion, never the source of truth. See `config.yaml` for a full example (one or more M365
tenants).

### Entra app registration

The collector calls `GET /v1.0/subscribedSkus` as the app registration configured by
`tenantId`/`clientId`/`clientSecret`. That app registration must be granted the Microsoft
Graph **application permission `Organization.Read.All`** (or the broader
`Directory.Read.All`), with **admin consent granted** in Entra ID.

## Demo stack

```bash
docker compose up
```

Brings up the exporter (`:9105`), Prometheus, and Grafana, auto-provisioned.

## Documentation

- Design spec: [`docs/superpowers/specs/2026-07-01-licenses-exporter-design.md`](docs/superpowers/specs/2026-07-01-licenses-exporter-design.md)
- Project conventions: [`CLAUDE.md`](CLAUDE.md)

## Development

```bash
make tools   # install golangci-lint, cyclonedx-gomod, govulncheck (pinned)
make ci      # gofmt check + vet + lint + race tests + govulncheck + build (the CI gate)
```

## License

Apache License 2.0 — see [LICENSE](LICENSE).
