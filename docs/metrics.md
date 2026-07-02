# Metrics reference

`licenses_exporter` exposes one generic `license_` metric family across every vendor —
vendors are distinguished by **labels**, not by metric name (see
[ADR-0004](adr/0004-generic-prefix-vendor-label-schema.md)). Every value is a raw fact
straight from the vendor API: there is no exporter-computed compliance verdict or
"days remaining" gauge (see [ADR-0005](adr/0005-raw-facts-absent-not-zero-naming-units.md)).
Derive those in PromQL or alert rules from the raw facts below.

This table is the diff target for `--once --debug`, which dumps every collected sample
(sorted, exposition style) for live payload validation against a real tenant/vCenter.

## License facts

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `license_seats_total` | Gauge | `vendor, product, unit, instance` | Total license capacity purchased/allocated. **Omitted** for unlimited/perpetual entitlements — never a `0` or `9999` sentinel. |
| `license_seats_used` | Gauge | `vendor, product, unit, instance` | Currently consumed/assigned license capacity. Always emitted when known. |
| `license_expiration_timestamp_seconds` | Gauge | `vendor, product, instance` | License expiration as a Unix timestamp. **Omitted entirely** when the license is perpetual (no per-SKU expiration). |

## Health / state

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `license_up` | Gauge | `vendor, instance` | `1` if the target's last collection cycle succeeded, `0` if it failed. Absent entirely until that source's first cycle resolves. |
| `license_collector_last_success_timestamp_seconds` | Gauge | `vendor, instance` | Unix timestamp of the last successful collection for this target. `time() - this` is the data-age/freshness signal. |
| `license_scrape_duration_seconds` | Gauge | `vendor, instance` | Wall-clock time spent collecting this target during the last cycle. |
| `license_build_info` | Gauge | `version, goversion` | Constant `1`; carries the exporter's build metadata. The only series present before the first collection cycle completes. |

## Label semantics

| Label | Meaning / source |
|---|---|
| `vendor` | `"microsoft"` or `"vmware"`. |
| `product` | M365: the SKU's `skuPartNumber` (e.g. `SPE_E5`). VMware: the license `name` (e.g. `vSphere_ENT+`). Raw vendor identifiers in v1 — no friendly-name mapping. |
| `unit` | M365: always `users`. VMware: the license's `costUnit` (e.g. `cpuPackage`, `cores`, `server`, `vm`). |
| `instance` | The configured target id from `config.yaml` (e.g. `tenant-a`, `vcsa01`). One process can poll many targets of the same vendor. |

## Design rules (raw facts, absent-not-zero)

- **No `days_to_expiration` gauge, no perpetual sentinel.** `license_expiration_timestamp_seconds`
  carries the absolute Unix timestamp; a perpetual license omits the series entirely. Compute
  days remaining in PromQL: `(license_expiration_timestamp_seconds - time()) / 86400`.
- **No exporter-computed `compliance_status`.** Over-allocation is
  `license_seats_used > license_seats_total`; policy belongs in PromQL/alert rules, not the
  exporter.
- **Absent, never zero.** An unparseable or missing capacity/used value yields an *absent*
  sample, never a fake `0` — a false `0` on a capacity metric would silently corrupt
  dashboards and over-allocation alerts.
- **VMware unlimited licenses.** vSphere encodes unlimited capacity as `Total <= 0` (eval /
  site / academic keys). The collector omits `license_seats_total` for that product and emits
  only `license_seats_used`; detect it in PromQL with `absent(license_seats_total{...})`.
- **Cold start.** Immediately after startup, before any source's first collection cycle
  resolves, `/metrics` exposes **only** `license_build_info` — no `license_up` or per-target
  series exist yet, so a scrape during that window can never see a transient `0` or a
  flapping target.
- **Label-key consistency.** Every series of a given metric name carries the same label-key
  set across all vendors, built from the shared builders in `internal/license/metrics.go`
  (see [ADR-0006](adr/0006-label-key-consistency-invariant.md)).

## Live validation

```bash
./bin/licenses_exporter --config config.yaml --once --debug
```

Runs a single collection cycle and prints every collected sample in sorted, Prometheus
exposition-style output — diff it against the tables above to catch a silently-absent
metric that `license_up` alone would not reveal.
