# 0002. Decouple vendor-API polling from scrapes with a snapshot model

- **Status:** accepted
- **Date:** 2026-07-02
- **Deciders:** Fred Jacquet

## Context and problem statement

`licenses_exporter` polls enterprise control planes — Microsoft Graph and vCenter's
`LicenseManager` — that are slow-moving (seat counts and license expirations change rarely)
but that impose real cost per call: a Graph app registration is rate-limited, and a vCenter
session login/logout pair has real latency and a session-timeout budget (~30 min idle).
Prometheus deployments scrape `/metrics` on their own cadence, frequently and potentially from
multiple replicas. If `/metrics` triggered a live vendor-API call per scrape, backend load and
session count would scale with scrape frequency rather than with how often license data
actually changes.

## Considered options

- **Collect-on-scrape** — the `/metrics` handler queries every tenant/vCenter synchronously
  on each scrape.
- **Snapshot model** — a background loop polls on `collection.interval` and publishes an
  immutable snapshot that both export paths read.

## Decision outcome

Chosen option: **the snapshot model**. A single background `Collector`
(`internal/license/collector.go`) fans out over every enabled `Source` (one per configured
tenant/vCenter) on `collection.interval` (default 2h) using an `errgroup` with a concurrency
cap, builds one immutable `Snapshot`, and pointer-swaps it into a `SnapshotStore` guarded by
an `RWMutex`. Both export paths read the latest snapshot rather than triggering their own
collection:

- `/metrics` (`prometheus.go`) is an *unchecked* Prometheus collector that reads the latest
  snapshot on every scrape.
- The OTLP push path (`otlp.go`) registers observable gauges whose callbacks read the same
  snapshot on the periodic reader's cadence.

`--once` runs a single `CollectOnce` cycle (optionally with `--debug` dumping every sample)
and exits instead of serving. HTTP is served **before** the first collection cycle completes
(login/first-poll latency must never block `/metrics` or `/health`); the store starts with a
cold-start snapshot containing only `license_build_info` until each source's first cycle
resolves.

### Consequences

- Good — vendor-API load and vCenter session count are a function of `collection.interval`,
  independent of scrape frequency or scraper replica count.
- Good — `/metrics` and `/health` are always fast and never block on a slow or unreachable
  tenant/vCenter; both export paths share one consistent view of the data.
- Good — graceful degradation: one source's failure (`license_up{vendor,instance}=0`) never
  blocks the snapshot for every other source in the same cycle.
- Bad — metrics can be up to one `collection.interval` stale; `license_collector_last_success_timestamp_seconds`
  makes that age observable rather than hiding it.
- Neutral — both export paths must tolerate the cold-start snapshot (only `license_build_info`,
  no target series yet); the design treats that as a real, intentional first state rather than
  an edge case to special-case away.

## Related

- [0006. One label-key set per metric name](0006-label-key-consistency-invariant.md)
- [0008. Config hot reload](0008-config-hot-reload.md)
- [0009. OTLP observation-time points](0009-otlp-observation-time-vs-snapshot-time.md)
