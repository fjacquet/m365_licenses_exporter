# 0009. OTLP push: observation-time points, not snapshot-time

- **Status:** accepted
- **Date:** 2026-07-02
- **Deciders:** Fred Jacquet
- **Supersedes:** the design spec's initial snapshot-time proposal (superseded 2026-07-01,
  during design review, before any code shipped)

## Context and problem statement

The dual-export design (design spec §2; [ADR-0002](0002-prometheus-snapshot-model.md)) reads
license data from a single snapshot that can be up to `collection.interval` (default 2h) old
by the time it is exported. For the Prometheus `/metrics` path this is transparent — a scrape
gets whatever the snapshot holds, timestamped at scrape time, exactly like any other
Prometheus exporter. For the **OTLP push path**, the initial design spec draft proposed
stamping each pushed data point with the snapshot's *collection* time, to make the point's
timestamp reflect when the underlying fact was actually true. A design review (review
addendum §2.6) found that proposal unworkable against the OTel-Go SDK as actually used here.

## Considered options

- **Snapshot-time points** — back-date every OTLP data point to the snapshot's collection
  timestamp, so the point's own timestamp reflects data freshness.
- **Observation-time points + an explicit freshness metric** — keep points timestamped at
  the OTLP reader's observation time (exactly like a Prometheus scrape), and convey data age
  explicitly via `license_collector_last_success_timestamp_seconds`.

## Decision outcome

Chosen option: **observation-time points + an explicit freshness metric** — this decision
**supersedes** the design spec's initial snapshot-time proposal, resolved during design
review before implementation began.

- The OTLP path stays on the family-standard pattern: `Float64ObservableGauge` metrics
  registered against a periodic `MeterProvider` reader (`internal/license/otlp.go`,
  `RegisterOTLP`). Each registered callback reads the current `SnapshotStore` value and calls
  `Observer.ObserveFloat64` for every matching sample — exactly like a Prometheus gauge
  callback, and with the same timestamp semantics: the point is stamped at **observation
  time** (when the periodic reader calls the callback), not at snapshot-collection time.
- **Why snapshot-time was rejected:** the OTel-Go SDK's observable-gauge + periodic-reader
  model does not support back-dating a point's timestamp without abandoning that model
  entirely for a manual `metricdata.Export()` loop — a real, non-trivial divergence from
  every other exporter in the family, which all use the same observable-gauge pattern.
- **Why it would have been actively harmful even if it were easy:** most OTLP metrics
  backends (Prometheus-OTLP receivers, Datadog, Dynatrace) enforce a timestamp-lookback
  window on ingestion, commonly around one hour. Stamping a point with a 0–2h-old snapshot
  timestamp would push a meaningful fraction of every collection cycle's data **outside**
  that window, silently **dropping** it at the backend rather than merely marking it stale.
  Observation-time points never hit that window at all.
- **Data age is not lost — it is made explicit.** `license_collector_last_success_timestamp_seconds{vendor,instance}`
  is emitted through both export paths from the same snapshot
  (`Collector.CollectOnce`/`LastSuccessSample`). Consumers compute
  `age = now - license_collector_last_success_timestamp_seconds` themselves — the same
  computation a Prometheus scrape-based consumer would already need for `time() -
  license_collector_last_success_timestamp_seconds`, so OTLP and Prometheus consumers use an
  identical freshness idiom.

### Consequences

- Good — the OTLP export path stays on the exact same observable-gauge/periodic-reader
  pattern as every other exporter in the family; no bespoke manual-export loop to maintain.
- Good — OTLP data points are never silently dropped by a backend's timestamp-lookback
  window, regardless of how stale the underlying snapshot is.
- Good — freshness/staleness is a first-class, queryable metric
  (`license_collector_last_success_timestamp_seconds`) identically available via both export
  paths, rather than being implicit in a point's own timestamp on only one of them.
- Bad — an OTLP consumer that assumes "point timestamp == when this fact was true" (as it
  might for a genuinely event-timestamped signal) will be misled by up to `collection.interval`;
  this must be documented wherever OTLP consumers are onboarded, not just here.
- Neutral — this ADR formally supersedes language in the original design spec §2 ("OTLP
  export specifics"); the spec text was updated in the same review round, so there is no
  surviving contradictory guidance to reconcile.

## Related

- [0002. Prometheus snapshot model](0002-prometheus-snapshot-model.md)
- Design review addendum, §2.6 (`docs/superpowers/specs/2026-07-01-licenses-exporter-review.md`)
