# licenses_exporter — Design Spec (v1)

- **Date:** 2026-07-01
- **Status:** Approved for planning
- **Repo:** `github.com/fjacquet/licenses_exporter`
- **Family:** Fred's Go Prometheus + OTLP exporter family (see the `exporter-standards` skill)

## 1. Purpose & scope

A **unified enterprise-license exporter**: a single Go binary that periodically
polls multiple enterprise control planes, normalizes their licensing/seat data
into one generic Prometheus schema, and exposes it (Prometheus scrape + OTLP push)
for a FinOps/IT-asset-management view inside the standard Prometheus/Grafana stack.

This fills a real open-source gap: M365 license exporters exist (`cloudeteer/m365-exporter`),
but no open-source VMware exporter tracks the vCenter **LicenseManager** (core/socket
allocation, expiration), and `windows_exporter --collector.ad` tracks DC *health*, not
identity/seat realities. A unified exporter puts VMware core consumption next to M365
seat usage next to (later) stale-AD accounts on one dashboard.

### In scope (v1 — this spec)

- The exporter **skeleton** with full family conformance (§8).
- The `Source` collector abstraction + config-driven registry (§3, §5).
- **Two reference collectors: Microsoft 365 and VMware vSphere** (§6).
- The generic raw-facts metric schema + health metrics (§4).
- The one-command docker-compose + Prometheus + **Grafana** demo stack (§7, §8).

### Out of scope (later incremental specs — §9)

GitHub, Atlassian, Veeam, Slack, on-prem Active Directory (LDAP), Entra ID identity
metrics. Each is its own spec + ADR reusing the `Source` interface with no expected
core change — that is the test of the abstraction.

### Non-goals

- No exporter-computed compliance verdict or "days remaining" gauge — those are
  derived in PromQL/alert rules from raw facts (§4).
- No live-on-scrape fetching — the snapshot model decouples backend load from scrapers.
- Not a replacement for infra-performance VMware exporters; this is license-only.

## 2. Architecture — snapshot collection model

The canonical family model (ADRs: `ppdd` 0001, `pstore` 0002), unchanged:

```
background loop (every collection.interval)
   → errgroup fan-out over all enabled Sources (SetLimit caps concurrency)
   → each Source returns []Sample (pre-stamped vendor,product,unit,instance)
   → build one immutable Snapshot
   → SnapshotStore.Swap()  (RWMutex pointer-swap)
        ├── PromCollector  (/metrics, unchecked collector — reads latest snapshot)
        └── OTLPExporter   (periodic push — observable gauges read same snapshot)
```

- **Serve HTTP + `/health` before the first collection cycle** (login/first-poll can
  exceed the collection timeout; blocking startup stalls `/metrics`). ADR `pstore` 0007.
- **Cold-start snapshot (absent-not-zero at startup).** The `SnapshotStore` is
  initialized with a cold-start snapshot carrying **only** exporter self-metrics
  (`license_build_info`). No `license_up`, `license_seats_*`, or any per-target series is
  emitted until each source's **first** collection resolves (success *or* failure) — so a
  scrape during the cold window never sees a transient `0` or a flapping target. `/health`
  reports `starting` until the first full cycle completes, then `ok`.
- **Per-source failure degrades gracefully** — a failed vCenter/tenant emits
  `license_up{vendor,instance}=0` and the cycle continues for every other source;
  one bad target never fails the whole cycle.
- **Optional knobs** (functional options): max concurrent sources (`errgroup.SetLimit`).
- Default `collection.interval`: **2h** (license data is near-static; 1–4h is the band).
- **Dual export** both tested: collector tests assert via **both** the Prometheus
  registry gather **and** an OTLP `ManualReader`.
- Config **hot reload**: SIGHUP + file-watch, rebuild-and-swap. ADR `ppdd` 0005.
  Each collection cycle runs under a **cancelable `context.Context`**. On reload the active
  cycle's context is canceled (in-flight SDK/HTTP requests abort), config is re-parsed and
  **validated**, and a **fresh loop is spawned immediately** (the 2h timer resets). The
  **last-good snapshot keeps serving** throughout — swapped only when the new loop produces
  its first snapshot — so `/metrics` never goes blank across a reload. A reload that fails
  validation is **rejected**; the running loop is left intact.

### OTLP export specifics

- **Resource attributes** on the OTLP `MeterProvider`: `service.name="licenses_exporter"`,
  `service.version=<build version>`, `service.instance.id=<hostname or configured id>`.
- **Timestamps.** Metrics are OTLP **observable gauges** read by a periodic reader, so each
  point carries the reader's **observation time** — observable-gauge points cannot be
  back-dated to the snapshot's collection time, and doing so is not idiomatic. Data
  *freshness/age* is conveyed explicitly by `license_collector_last_success_timestamp_seconds`
  (age = `now - last_success`), which is exactly why that metric is first-class rather than a
  back-dated point timestamp.

## 3. The `Source` collector abstraction

The family stamps one identity label from the loop (`ppdd`'s `ResourceCollector` +
`Registry()`). This exporter fans out over multiple tenants/vCenters, so the unit of
work is a **Source** (one configured target of one vendor), not a whole vendor:

```go
// Source collects license facts from a single configured target. It returns
// samples already stamped with vendor+instance (+product+unit); the loop only
// aggregates them into the snapshot.
type Source interface {
    Vendor() string   // "microsoft" | "vmware"
    Instance() string // "tenant-a"  | "vcsa01"
    Collect(ctx context.Context) ([]Sample, error)
}
```

- Each vendor package exposes a constructor: `func NewSources(cfg VendorConfig) ([]Source, error)`
  that builds one `Source` per configured target.
- The registry = the concatenation of every enabled vendor's `[]Source`.
- A `Sample{Name, []Label, Value}` unifies output; shared label builders live in
  `internal/license/metrics.go` so the label-key invariant holds across vendors.

## 4. Metric schema (raw facts — "absent, never zero/sentinel")

Prefix **`license_`** (singular prefix from the plural repo name is deliberate, exactly
like `obs_exporter` → `ecs_`).

```
# license facts
license_seats_total{vendor,product,unit,instance}                 # capacity purchased
license_seats_used{vendor,product,unit,instance}                  # consumed/assigned
license_expiration_timestamp_seconds{vendor,product,instance}     # Unix ts; OMITTED when perpetual

# health / state
license_up{vendor,instance}                                       # 1 ok, 0 last refresh failed
license_collector_last_success_timestamp_seconds{vendor,instance}
license_scrape_duration_seconds{vendor,instance}
license_build_info{version,goversion,...}                         # constant 1
```

**Label semantics**

| Label | Source |
|---|---|
| `vendor` | `"microsoft"`, `"vmware"` |
| `product` | M365 `skuPartNumber` (e.g. `SPE_E5`); VMware license `name`. Raw in v1 (friendly-name mapping deferred). |
| `unit` | M365 → `users`; VMware → the license `costUnit` (`cpuPackage`/`cores`/`server`/`vm`). |
| `instance` | the configured target id (`tenant-a`, `vcsa01`) — one process, many targets. |

**Design rules (novel → ADRs):**
- **No `days_to_expiration` gauge and no `+9999` perpetual sentinel.** Expose the
  absolute Unix timestamp; perpetual licenses **omit** the series entirely. Days-left is
  `(license_expiration_timestamp_seconds - time()) / 86400` in PromQL.
- **No exporter-computed `compliance_status`.** Over-allocation (`used > total`) and
  expiry are one-line alert/PromQL expressions — policy is not baked into the exporter.
- **Absent, never zero:** an unparseable capacity/used value yields an *absent* sample,
  never a fake `0` (a fake 0 on a capacity metric silently corrupts dashboards/alerts).
  Tolerant payload parsing localized per vendor package. (`obs` ADR-0007.)
- **Label-key consistency invariant:** every series of a metric name carries the same
  label-key set, built from the shared builders; enforced by a test.

## 5. Configuration (`config.yaml` is the way; `.env` is nice)

`${ENV_VAR}` expansion in host/username/password/secret (fail-fast on unset) +
`passwordFile` for secrets; `.env` loaded natively at startup (never overrides real env).

```yaml
collection:
  interval: 2h
collectors:
  m365:
    enabled: true
    tenants:
      - instance: tenant-a
        tenantId: ${M365_TENANT_ID}
        clientId: ${M365_CLIENT_ID}
        clientSecret: ${M365_CLIENT_SECRET}   # or clientSecretFile
  vmware:
    enabled: true
    vcenters:
      - instance: vcsa01
        host: ${VCENTER_HOST}
        username: ${VCENTER_USER}
        password: ${VCENTER_PASSWORD}          # or passwordFile
        insecureSkipVerify: false
```

- Collector toggling is **`enabled:` per collector**, not an `ENABLED_COLLECTORS` env var
  — config.yaml stays the source of truth. Multi-target = one list entry per target.
- Compose passes `M3651_*` / `VMWARE1_*` literal-default vars for the single-target
  quickstart; env passthrough never replaces config.yaml.

## 6. The two v1 collectors & client choices (ADR-0003)

The family SDK rule: **official vendor Go SDK if available AND useful, else hand-roll
`resty/v2`.**

### VMware — `govmomi` (SDK) — clean SDK-yes

- Available + **useful**: current session-auth flow; `LicenseManager.licenses` is a
  **single property-collector fetch** (no N+1), fully typed
  (`LicenseManagerLicenseInfo` → `Total`, `Used`, `CostUnit`, `Properties` incl.
  `expirationDate`). No regression. → **use govmomi.**
- Map: `Total`→`seats_total`, `Used`→`seats_used`, `CostUnit`→`unit`, `Name`→`product`,
  `expirationDate` property → `expiration_timestamp_seconds` (omit if absent/perpetual).
- **Stateless session per cycle.** Polling is every 2h and idle vSphere sessions time out
  (~30 min default), so the VMware `Source` is fully stateless: each cycle does a fresh
  `Login` → single `LicenseManager` PropertyCollector query → `Logout` + close. No persisted
  cookie, no session-refresh code. Recorded in the client ADR.
- **Unlimited licenses → omit `seats_total`.** vSphere encodes unlimited capacity as
  `Total == 0` (eval / site / academic keys). Emitting `seats_total=0` would be a false
  zero-capacity *and* a spurious `used > total`. Per absent-not-zero, treat `Total <= 0` as
  unlimited: **omit `license_seats_total`** for that product, emit only `license_seats_used`;
  PromQL detects it with `absent(license_seats_total{...})`. The `<= 0` guard is deliberate so
  the exact sentinel (`0` vs `-1`) is confirmed against `vcsim` + a real vCenter at impl (§11).

### Microsoft 365 — `msgraph-sdk-go` (SDK) — roadmap-justified

- Endpoint: `GET /v1.0/subscribedSkus` → `skuPartNumber`→`product`, `prepaidUnits.enabled`
  →`seats_total`, `consumedUnits`→`seats_used`, `unit="users"`. M365 subscription SKUs
  generally have no per-SKU expiration via this endpoint → `expiration` series omitted.
- **Required Graph application permission:** `Organization.Read.All` (or `Directory.Read.All`)
  granted to the Entra ID app registration — documented in `docs/deployment/` so operators
  pre-grant it before first run.
- **Pagination:** follow `@odata.nextLink` via the SDK's `PageIterator`, even though
  `subscribedSkus` rarely pages — never assume a single page for large tenants.
- **Deviation recorded honestly:** the strict rule would hand-roll `resty` + `azidentity`
  for one endpoint (the full Graph SDK is a heavy generated dep tree — the "irrelevant
  dependency tree" regression). Chosen anyway as a **forward-looking exception**: phase-2
  Entra ID collectors (users, `signInActivity`, MFA status) will lean heavily on Graph, so
  the SDK's dep-tree cost amortizes across the identity domain. Auth is `azidentity`
  client-credentials (the SDK uses it under the hood). ADR-0003 states this explicitly.

### Auth & the `--trace` token-leak caveat

Both SDKs are **non-injectable** transports, so — exactly like `pstore`/`pscale` — SDK
debug modes leak credentials (bearer / `Set-Cookie`); never enable them. `--trace` wraps
only repo-owned transports; the ADR documents the typed-SDK trace gap. Retry **excludes
4xx** (never retry auth failures). ADR `ppdd` 0004.

## 7. Live-validation tooling (family requirement)

- `--once --debug` — print every collected sample (sorted, exposition style) to diff
  against `docs/metrics.md`; catches silently-absent metrics that `_up` can't.
- `--trace` — log each repo-owned API response (method/path/status/body), skipping any
  credential-bearing response; hand-rolled `OnAfterResponse` hook, never SDK debug.

## 8. Family conformance (all required deliverables)

- **Go `1.26.4`** patch-pinned; CGO off for release; `make tools` pins dev tools.
- **Layout:** `main.go` (cobra `--config --debug --once --trace`, HTTP before first
  collect, SIGHUP + file-watch); `internal/license/` (`snapshot.go`, `collector.go` loop,
  `prometheus.go` unchecked, `otlp.go`, `metrics.go` shared builders, `source.go`,
  `state.go`); `internal/m365/`, `internal/vmware/` (one package per vendor: config +
  `NewSources` + `Source` impls + payload parsing); `internal/{config,logging,telemetry}`.
- **Makefile contract:** `tools fmt-check fmt vet lint test test-race test-coverage vuln
  ci sure cli sbom release release-snapshot docker run-cli clean`. `make ci` green (gofmt,
  vet, golangci-lint, `go test -race`, govulncheck).
- **CI/CD — consume `fjacquet/ci@v1`:** four thin caller stubs `ci.yml`→`go-ci.yml@v1`,
  `security.yml`→`go-security.yml@v1`, `release.yml`→`go-release.yml@v1` (on `v*`),
  `docs.yml`→`docs-publish.yml@v1`. Secrets: `CODECOV_TOKEN`, `HOMEBREW_TAP_GITHUB_TOKEN`
  (optional). Do **not** re-inline workflows or SHA-pin (that lives in `fjacquet/ci`).
- **Owned by repo:** `.goreleaser.yaml` (`version: 2`, CGO off, linux/darwin × amd64/arm64,
  cyclonedx-gomod SBOM, checksums, self-skipping Homebrew cask), `Dockerfile` (multi-stage,
  **non-root**, Alpine + copied CA certs), `Dockerfile.goreleaser`, `.github/dependabot.yml`
  (**gomod + docker only**).
- **Port 9105** (free, adjacent to `pmax` 9104).

### Observability quickstart (docker-compose + Prometheus + Grafana) — required

- `docker-compose.yml` — exporter (built from `./Dockerfile`) + Prometheus + Grafana,
  auto-provisioned; `docker-compose.ghcr.yml` — same, pulling `ghcr.io/fjacquet/licenses_exporter:latest`.
- `prometheus.yml` scrape job (port 9105) + `deploy/prometheus/license.rules.yml`:
  - `LicenseOverAllocated`: `license_seats_used > license_seats_total`
  - `LicenseExpiringSoon`: `license_expiration_timestamp_seconds - time() < 30*86400`
  - `LicenseCollectorDown`: `license_up == 0`
- **Grafana** provisioning:
  - `grafana/provisioning/datasources/datasource.yml` (Prometheus datasource)
  - `grafana/provisioning/dashboards/dashboards.yml` (provider)
  - `grafana/dashboards/licenses-overview.json`
  - Template vars: `vendor`, `instance`, `product`. Panels (all off raw facts):

    | Panel | Query |
    |---|---|
    | Seat utilization % | `100 * license_seats_used / license_seats_total` |
    | Over-allocated (table) | `license_seats_used > license_seats_total` |
    | Seats free (stat) | `license_seats_total - license_seats_used` |
    | Days to expiration (table, asc) | `(license_expiration_timestamp_seconds - time()) / 86400` |
    | Expiring < 30d | `license_expiration_timestamp_seconds - time() < 30*86400` |
    | Collector health | `license_up{vendor=~"$vendor",instance=~"$instance"}` |
    | Last refresh age | `time() - license_collector_last_success_timestamp_seconds` |

  - Perpetual licenses omit `license_expiration_timestamp_seconds`, so they simply don't
    appear in the expiration panels — no `9999`-year rows.
- `docs/dashboards.md` + `docs/deployment/docker.md` document the stack (MkDocs nav).

### Docs & ADRs

- `CLAUDE.md` (overview, commands, architecture, constraints, testing, CI/CD).
- MkDocs Material site; `docs/metrics.md` catalog.
- `docs/adr/NNNN-title.md`: supply-chain; snapshot model; **generic-prefix + vendor-label
  schema [novel]**; **naming/units: raw-facts, absent-not-zero, no sentinel [novel]**;
  client choice (govmomi SDK + msgraph SDK roadmap exception) `0003`; label-key invariant;
  token auth + retry; config hot reload; serve-HTTP-before-first-collect.

## 9. Testing (TDD)

- **VMware:** govmomi's **`vcsim`** simulator provides a real `LicenseManager` — fixture
  the license list **including an unlimited `Total==0` key and a dated key**, and assert
  both the omitted-`seats_total` and the expiration-timestamp behaviors. Also assert the
  cold-start snapshot (only `license_build_info` before first collect) and reload
  (snapshot continuity + context cancellation).
- **M365:** mock the Graph client interface (or `httptest` behind the SDK's transport)
  returning canned `subscribedSkus` payloads incl. malformed values (absent-not-zero).
- Every collector test asserts via **both** the Prometheus registry gather and an OTLP
  `ManualReader`.
- Label-parity test enforcing the label-key invariant across both vendors.
- Semgrep clean — **no inline suppressions** (restructure instead).

## 10. Phasing (this is a program of work)

- **Spec #1 (this doc):** skeleton + `Source` abstraction + M365 + VMware + full
  conformance + demo stack.
- **Later specs, one each, reusing `Source` with no core change expected:** on-prem AD
  (LDAP: stale/disabled/OS-count metrics — a *different* metric family, e.g.
  `identity_*`, decided in its own spec), Entra ID (Graph: user types, inactive sign-ins,
  MFA), GitHub (`/orgs/{org}/settings/billing/licenses`), Atlassian
  (`/rest/api/3/applicationrole`), Veeam (`/api/v1/license`), Slack (`team.billableInfo`).
- The AD/Entra identity metrics are intentionally deferred: they are asset/identity
  facts, not license seats, and warrant their own schema ADR rather than being forced
  into `license_*`.

## 11. Open items carried into planning

- Confirm port **9105** is unclaimed at deploy time (docs list it as free).
- VMware expiration extraction: `expirationDate` lives in `LicenseManagerLicenseInfo.Properties`
  as a `KeyAnyValue` — confirm the key/format against `vcsim` + a real vCenter during impl.
- M365 SKU expiration is generally absent from `subscribedSkus`; if a tenant exposes it
  elsewhere it is a later enhancement, not v1.
- VMware unlimited-capacity sentinel: spec assumes `Total == 0` = unlimited; confirm the
  exact encoding (`0` vs `-1`) against `vcsim` + a real vCenter and keep the `<= 0` guard.
