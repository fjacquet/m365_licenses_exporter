# Architecture decision records

This directory records the significant architectural decisions for `licenses_exporter` — the
*why* behind the design, in the form of dated [MADR](https://adr.github.io/madr/)-style
records. Decisions are immutable once accepted: rather than editing a past record, add a new
one that supersedes it.

| ADR | Decision | Status |
|---|---|---|
| [0001](0001-supply-chain-release-hardening.md) | Supply-chain / release hardening: reusable-workflow CI, GoReleaser, SBOM | accepted |
| [0002](0002-prometheus-snapshot-model.md) | Decouple vendor-API polling from scrapes with a snapshot model | accepted |
| [0003](0003-client-choice-govmomi-sdk-and-msgraph-sdk.md) | Client choice: `govmomi` SDK (clean fit) and `msgraph-sdk-go` (roadmap-justified exception) | accepted |
| [0004](0004-generic-prefix-vendor-label-schema.md) | One generic `license_` prefix; vendors distinguished by labels | accepted |
| [0005](0005-raw-facts-absent-not-zero-naming-units.md) | Raw facts, absent-not-zero, no exporter-computed compliance/day-counts | accepted |
| [0006](0006-label-key-consistency-invariant.md) | One label-key set per metric name | accepted |
| [0007](0007-token-auth-retry-policy.md) | Token/credential auth with a retry policy that excludes 4xx | accepted |
| [0008](0008-config-hot-reload.md) | Config hot reload: cancelable context + last-good-snapshot continuity | accepted |
| [0009](0009-otlp-observation-time-vs-snapshot-time.md) | OTLP push: observation-time points, not snapshot-time | accepted |

To add a decision, copy [`0009`](0009-otlp-observation-time-vs-snapshot-time.md)'s structure to
the next number and link it here.
