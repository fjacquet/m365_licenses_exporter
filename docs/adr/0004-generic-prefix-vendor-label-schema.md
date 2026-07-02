# 0004. One generic `license_` prefix; vendors distinguished by labels

- **Status:** accepted
- **Date:** 2026-07-02
- **Deciders:** Fred Jacquet

## Context and problem statement

The exporter family's established convention is one metric prefix **per vendor**
(`ecs_*`, `pflex_*`, `ppdd_*`, `pstore_*` — one exporter, one vendor, one prefix).
`licenses_exporter` is different in kind: it is a single binary that unifies **multiple**
vendors' license/seat facts (Microsoft 365 today; VMware vSphere today; GitHub, Atlassian,
Veeam, Slack later — design spec §10) behind one FinOps/IT-asset dashboard. Naming metrics
`m365_seats_total` and `vmware_seats_total` would fragment every dashboard panel and PromQL
query per vendor, defeating the exporter's entire reason to exist: one place to see license
posture across the enterprise.

## Considered options

- **Per-vendor prefix** (`m365_seats_total`, `vmware_seats_total`, …) — consistent with the
  rest of the family, but forces every panel/alert to be vendor-specific or to use
  `or`-chains across differently-named metrics.
- **One generic prefix, vendor as a label** (`license_seats_total{vendor="microsoft"}`,
  `license_seats_total{vendor="vmware"}`) — one query, one panel, one alert rule works across
  every vendor and every future addition.

## Decision outcome

Chosen option: **one generic `license_` prefix**, deliberately breaking the family's
per-vendor-prefix norm — exactly as `obs_exporter` derives `ecs_` from its plural repo name,
`licenses_exporter` derives the singular `license_` prefix. Vendors are distinguished purely
by the `vendor` label (`"microsoft"`, `"vmware"`, and future values), alongside
`product` (the vendor's own SKU/license name), `unit` (the vendor's capacity unit), and
`instance` (the configured target id). All four labels are built once, in
`internal/license/metrics.go`'s shared `SeatSample`/`ExpirationSample`/… builders, so every
vendor package stamps samples through the same code path rather than reinventing label
construction per vendor.

Adding a new vendor collector (design spec §10; `CLAUDE.md` "Adding a vendor collector")
never introduces a new metric name or prefix for license facts — only new `vendor` label
values. A genuinely different metric family (e.g., on-prem AD/Entra ID identity/asset facts)
is explicitly **out of scope for `license_*`** and gets its own prefix and schema ADR instead
of being force-fit here.

### Consequences

- Good — one dashboard, one PromQL expression, one alert rule (`LicenseOverAllocated`,
  `LicenseExpiringSoon`) works unmodified across every current and future vendor.
- Good — adding GitHub/Atlassian/Veeam/Slack later requires zero dashboard/alert changes if
  their seat data fits the same `seats_total`/`seats_used`/`expiration` shape — only a new
  `Source` implementation and `vendor` label value.
- Bad — this is a **deliberate deviation** from the rest of the exporter family's
  one-prefix-per-vendor convention; anyone porting patterns from a sibling exporter must not
  assume `license_*` should become `m365_*`/`vmware_*`.
- Neutral — vendor-specific quirks (e.g., VMware's `costUnit` vocabulary vs. M365's constant
  `"users"`) live entirely in the `unit`/`product` label *values*, never in the metric name or
  label *keys* — see [ADR-0006](0006-label-key-consistency-invariant.md).

## Related

- [0005. Raw facts, absent-not-zero, naming and units](0005-raw-facts-absent-not-zero-naming-units.md)
- [0006. One label-key set per metric name](0006-label-key-consistency-invariant.md)
