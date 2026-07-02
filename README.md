# licenses_exporter

[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

A unified enterprise-**license** exporter for the Prometheus/Grafana stack. It periodically
polls multiple enterprise control planes — **Microsoft 365** (Microsoft Graph) and
**VMware vSphere** (vCenter `LicenseManager`) — and normalizes their seat/entitlement data
into one generic Prometheus schema, exposed via **both** a Prometheus `/metrics` endpoint
and an OTLP metric push, fed from a single shared snapshot.

## Metrics

One `license_` prefix across all vendors; vendors are distinguished by labels, not by
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
./bin/licenses_exporter --config config.yaml
# metrics: http://localhost:9105/metrics   health: http://localhost:9105/health
```

Useful flags: `--once --debug` runs a single collection cycle and dumps every collected
sample (sorted, exposition style) instead of serving; `--trace` logs repo-owned API response
bodies for live payload validation (never SDK debug modes, which would leak the bearer
token / session cookie).

## Configuration

Collectors are toggled per-vendor in `config.yaml` (`collectors.m365.enabled` /
`collectors.vmware.enabled`), not via environment variables. Secrets are referenced as
`${ENV}` placeholders inside `config.yaml` (or via `passwordFile` / `clientSecretFile` for
file-based secrets); a `.env` file is a convenience for local `${ENV}` expansion, never the
source of truth. See `config.yaml` for a full example (M365 tenant + VMware vCenter).

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
