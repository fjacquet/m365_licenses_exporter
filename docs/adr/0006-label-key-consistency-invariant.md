# 0006. One label-key set per metric name

- **Status:** accepted
- **Date:** 2026-07-02
- **Deciders:** Fred Jacquet

## Context and problem statement

`/metrics` is served by an *unchecked* Prometheus collector (`prometheus.go`) so that the
set of samples can vary freely per snapshot (see [ADR-0002](0002-prometheus-snapshot-model.md)):
`client_golang` does **not** enforce a consistent variable-label-key set per metric name for
an unchecked collector the way a checked `Desc`-based collector would. Because
`licenses_exporter` deliberately shares one metric name across every vendor
([ADR-0004](0004-generic-prefix-vendor-label-schema.md)), the risk is concrete: if the VMware
and M365 packages ever built `license_seats_total` samples with different label-key sets (say,
one vendor omitting `unit`), a checked registry — or a real Prometheus server merging series —
would reject the scrape with an "inconsistent label cardinality" error, taking down every
other metric in the same scrape along with it.

## Considered options

- **Rely on discipline** — trust that every vendor package builds `license_*` samples with
  the same label keys by convention.
- **Enforce the invariant with shared builders + a test** — make it structurally hard to
  drift, and catch it at build time if it does.

## Decision outcome

Chosen option: **enforce the invariant**. The rule is: *a metric name carries exactly one
label-key set across every vendor's series.*

- **Shared builders are the only way to construct a sample.** `internal/license/metrics.go`'s
  `SeatSample` (`instance, product, unit, vendor` — sorted by key), `ExpirationSample`
  (`instance, product, vendor`), `UpSample`/`LastSuccessSample`/`ScrapeDurationSample`
  (`instance, vendor`), and `BuildInfoSample` (`goversion, version`) are the sole
  constructors every vendor package (`internal/vmware`, `internal/m365`) calls to produce a
  `Sample` — no vendor package builds a `Label` slice by hand.
- **Statically:** a label-parity test asserts that VMware and M365 samples for the same
  metric name carry identical label-key sets, run in CI on every change.
- **At runtime:** the Prometheus `unchecked` collector still tolerates a stray inconsistency
  without crashing the whole scrape, since it never registers a fixed `Desc` per metric name —
  the static test is the real gate; the runtime path is defense in depth, not the primary
  guarantee.

### Consequences

- Good — exported series shape is stable across vendors; a real scrape or OTLP export cannot
  fail due to per-vendor label-key drift.
- Good — the invariant is caught at build/test time when adding a vendor collector, not
  discovered in production against a real Prometheus server.
- Good — adding a vendor (design spec §10) is guided, not guessed: `CLAUDE.md`'s "Adding a
  vendor collector" section requires calling the shared builders, which makes conformance the
  path of least resistance.
- Bad — a genuinely novel label a future vendor needs (e.g., something with no M365/VMware
  analogue) cannot be added to one vendor's samples alone; it must either fit the existing
  builder or force a schema-level ADR update affecting every vendor.
- Neutral — label ordering inside each builder is fixed (alphabetical by key) purely for
  deterministic `--once --debug` output and test diffs; Prometheus itself does not care about
  label order.

## Related

- [0002. Prometheus snapshot model](0002-prometheus-snapshot-model.md)
- [0004. Generic prefix, vendor-label schema](0004-generic-prefix-vendor-label-schema.md)
