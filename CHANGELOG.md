# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project adheres to
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased] — v1

Initial release: a unified enterprise-license exporter for the Prometheus/Grafana stack.

### Added

- **Core exporter skeleton.** A background collection loop (`internal/license/collector.go`)
  fans out over every configured `Source` on `collection.interval` (default `2h`) and
  publishes an immutable snapshot to an `RWMutex`-guarded `SnapshotStore`. HTTP (`/metrics`,
  `/health`) is served **before** the first collection cycle completes; at cold start only
  `license_build_info` is exposed until each source's first cycle resolves. See
  [ADR-0002](docs/adr/0002-prometheus-snapshot-model.md).
- **Dual export from one snapshot.** A Prometheus `/metrics` endpoint (`prometheus.go`, an
  unchecked collector) and an OTLP/gRPC push exporter (`otlp.go`, observable gauges via a
  periodic reader; gated on `otlp.endpoint` being set) both read the same snapshot, so
  vendor-API load and vCenter session count never scale with scrape count. See
  [ADR-0009](docs/adr/0009-otlp-observation-time-vs-snapshot-time.md).
- **Generic `license_` metric schema.** One prefix across every vendor —
  `license_seats_total`, `license_seats_used`, `license_expiration_timestamp_seconds`
  (`vendor,product,unit,instance`/`vendor,product,instance`), plus health metrics
  `license_up`, `license_collector_last_success_timestamp_seconds`,
  `license_scrape_duration_seconds` (`vendor,instance`), and `license_build_info`
  (`version,goversion`). No exporter-computed `days_to_expiration` or `compliance_status`;
  raw facts are absent, never a fake `0` or sentinel, on an unparseable/unlimited/perpetual
  value. See `docs/metrics.md`, [ADR-0004](docs/adr/0004-generic-prefix-vendor-label-schema.md),
  [ADR-0005](docs/adr/0005-raw-facts-absent-not-zero-naming-units.md), and
  [ADR-0006](docs/adr/0006-label-key-consistency-invariant.md).
- **VMware vSphere collector** (`internal/vmware`) via `govmomi`: a fully stateless
  `Source` per configured vCenter — fresh login, a single `LicenseManager` property-collector
  fetch, immediate logout, every collection cycle. Unlimited licenses (`Total <= 0`) omit
  `license_seats_total` and emit only `license_seats_used`. See
  [ADR-0003](docs/adr/0003-client-choice-govmomi-sdk-and-msgraph-sdk.md).
- **Microsoft 365 collector** (`internal/m365`) via `msgraph-sdk-go` + `azidentity`
  client-credentials auth: paginated `subscribedSkus` retrieval
  (`@odata.nextLink`-following `PageIterator`) mapped to `seats_total`/`seats_used` with
  `unit="users"`. Requires the Graph application permission `Organization.Read.All` (or
  `Directory.Read.All`) — see `docs/deployment/docker.md`.
- **Configuration.** `config.yaml`-driven, per-collector `enabled:` toggling,
  `${ENV}` expansion (fail-fast on unset) in host/user/secret fields, `passwordFile` /
  `clientSecretFile` for file-based secrets, and a `.env` convenience loader that never
  overrides real environment variables.
- **Cancelable, validated hot reload.** The serving stack — shared `SnapshotStore`, OTLP
  exporter, `/health`, and a single bound HTTP listener — is built **once** (`app.NewServer`);
  a `SIGHUP` or config file change cancels only the active collection cycle's context,
  validates the candidate config before touching anything, and respawns just the collection
  loop (`Server.RunCollection`) on the same server and store. `/metrics` keeps serving the
  last-good snapshot throughout (never blanks to `build_info`-only), `/health` never flips back
  to `503`, the socket is never rebound (no reload-time "address already in use"), and a config
  that fails validation is rejected without disturbing the running exporter. `RunCollection`
  runs exactly one collect before its ticker — no double initial collect on startup/reload.
  See [ADR-0008](docs/adr/0008-config-hot-reload.md).
- **Live-validation flags.** `--once --debug` dumps every collected sample (sorted,
  exposition style) for a direct diff against `docs/metrics.md`; `--trace` logs repo-owned
  API responses without ever enabling SDK-level debug output (which would leak the bearer
  token / session cookie). See [ADR-0007](docs/adr/0007-token-auth-retry-policy.md).
- **Family conformance.** Go `1.26.4`, `Makefile` contract (`tools fmt-check fmt vet lint
  test test-race test-coverage vuln ci sure cli sbom release release-snapshot docker run-cli
  clean`), a non-root multi-stage `Dockerfile` (Alpine, uid `10001`), GoReleaser
  (`.goreleaser.yaml`) cross-compiling `linux,darwin × amd64,arm64` with CycloneDX SBOM,
  checksums, and a self-skipping Homebrew cask, and four thin CI/CD caller stubs consuming
  `fjacquet/ci@v1` reusable workflows. See
  [ADR-0001](docs/adr/0001-supply-chain-release-hardening.md).
- **Observability demo stack.** `docker compose up` brings up the exporter (`:9105`),
  Prometheus (`:9090`, scraping the exporter and loading
  `deploy/prometheus/license.rules.yml`'s `LicenseOverAllocated` / `LicenseExpiringSoon` /
  `LicenseCollectorDown` alerts), and Grafana (`:3000`, auto-provisioned with the
  **Enterprise Licenses — Overview** dashboard,
  `grafana/dashboards/licenses-overview.json`). A `docker-compose.ghcr.yml` variant runs the
  published image instead of building locally. See `docs/dashboards.md` and
  `docs/deployment/docker.md`.
- **Docs.** MkDocs Material site (`mkdocs.yml`) publishing the metrics catalog, deployment
  and dashboard guides, and nine architecture decision records
  (`docs/adr/0001`–`0009`, indexed at `docs/adr/index.md`).

### Fixed

- **M365 SKUs with no `skuPartNumber` are skipped** rather than emitted with `product=""`,
  which would have collapsed distinct unidentifiable SKUs onto one series (label-contract /
  raw-facts, [ADR-0005](docs/adr/0005-raw-facts-absent-not-zero-naming-units.md)).
- **VMware `Logout` is bounded by a 10s timeout** (fresh context) so a stalled TCP can never
  block the deferred call indefinitely, and a logout failure is now logged for session-leak
  visibility.
- **HTTP server hardening.** The `/metrics`+`/health` server now sets `ReadTimeout`,
  `WriteTimeout`, and `IdleTimeout` in addition to `ReadHeaderTimeout` (slowloris resistance).
- **`make docker` now passes `--build-arg VERSION`** so locally built images report the real
  version in `license_build_info` instead of `dev`.
- **Config file-watcher setup failures are surfaced** (a failed `fsnotify.NewWatcher`/`Add` is
  logged, noting reload still works via `SIGHUP`), and the watcher's `Errors` channel is drained
  and logged instead of being left to fill.
