# 0005. Raw facts, absent-not-zero, naming and units

- **Status:** accepted
- **Date:** 2026-07-02
- **Deciders:** Fred Jacquet

## Context and problem statement

License data invites an exporter to get "helpful" and pre-compute derived facts: days until
expiration, a compliance verdict, a synthetic "unlimited" capacity number. Every one of those
choices bakes a policy decision (what counts as "soon", what counts as "compliant") into the
exporter binary, where it cannot be tuned per organization without a code change and a
redeploy — and a badly-chosen sentinel value (`0`, `-1`, `9999`, `math.MaxInt32`) silently
corrupts any PromQL expression or dashboard panel that does arithmetic on it.

## Considered options

- **Exporter-computed derived metrics** — emit `license_days_to_expiration` and
  `license_compliance_status` directly, with sentinels for perpetual/unlimited cases.
- **Raw facts only, absent instead of a sentinel** — emit only what the vendor API actually
  reports, as absolute values with correct units; omit a series entirely when the underlying
  fact does not apply, and let PromQL/alert rules derive anything else.

## Decision outcome

Chosen option: **raw facts only, absent instead of a sentinel**, applied consistently across
both vendors and both metric types:

- **No `days_to_expiration` gauge.** `license_expiration_timestamp_seconds` carries the
  absolute Unix timestamp; `(license_expiration_timestamp_seconds - time()) / 86400` in
  PromQL is the "days remaining" the family's `deploy/prometheus/license.rules.yml`
  `LicenseExpiringSoon` alert and the dashboard's "Days to expiration" panel both use.
- **No `compliance_status` gauge.** Over-allocation is one PromQL comparison,
  `license_seats_used > license_seats_total` (the `LicenseOverAllocated` alert and the
  dashboard's "Over-allocated licenses" panel) — the exporter never decides what "compliant"
  means for an organization.
- **Perpetual licenses omit the expiration series entirely** — no `+9999`-year sentinel. M365
  subscription SKUs generally have no per-SKU expiration via `subscribedSkus` at all, so that
  series is simply never emitted for them; a VMware license with no `expirationDate` property
  behaves identically.
- **VMware unlimited capacity (`Total <= 0`) omits `license_seats_total`**, emitting only
  `license_seats_used` for that product (`internal/vmware/parse.go`'s `licensesToSamples`).
  vSphere encodes eval/site/academic unlimited keys as `Total == 0`; emitting `0` would read
  as zero *capacity* (worse than useless) and would spuriously trip
  `used > total`. `absent(license_seats_total{...})` is the PromQL idiom for detecting an
  unlimited product.
- **Absent, never a fake zero, on parse failure.** An unparseable or missing capacity/used
  value (a malformed Graph payload field, a missing govmomi property) yields an absent
  sample for that specific series, never a synthetic `0` — a false `0` on a capacity metric is
  strictly worse than a temporary gap, because it silently corrupts both dashboards and
  alerts rather than just being stale.
- **Units are the vendor's own.** `unit` is `"users"` for M365 and the license's own
  `costUnit` (e.g. `cpuPackage`, `cores`, `server`, `vm`) for VMware — never normalized or
  translated, since there is no single canonical seat unit across vendors.

### Consequences

- Good — no policy decision (what is "soon", what is "compliant") is hard-coded into the
  binary; every organization tunes its own PromQL/alert thresholds without a redeploy.
- Good — no sentinel value can ever be misread as a real capacity or a real expiration date.
- Good — a parsing bug degrades to a missing data point (visible as a gap or via
  `absent()`), never to a wrong number that looks legitimate.
- Bad — consumers must know the `absent()`/arithmetic PromQL idioms above; the raw metrics
  alone do not answer "is anything expiring soon" without a query.
- Neutral — the VMware `<= 0` guard (rather than strictly `== 0`) is deliberate slack for an
  as-yet-unconfirmed negative sentinel; it is re-validated against `vcsim` and a real vCenter
  in the VMware collector's test suite, not assumed from documentation alone.

## Related

- [0004. Generic prefix, vendor-label schema](0004-generic-prefix-vendor-label-schema.md)
- [0006. One label-key set per metric name](0006-label-key-consistency-invariant.md)
