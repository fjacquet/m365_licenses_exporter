# licenses-exporter-core + m365 split — Design Spec

**Status:** Draft for review · **Date:** 2026-07-02 · **Author:** Fred Jacquet (with Claude)

**Goal:** Extract the generic engine of `licenses_exporter` into a reusable Go
module, `licenses-exporter-core`, so that licensing can be exported by **one
thin per-vendor exporter per corporation** (VMware, Microsoft 365, Veeam, …)
that all share a single `license_` schema — while reshaping the current repo
into the first consumer, `m365_licenses_exporter`.

**Architecture:** A shared core module owns everything vendor-neutral (schema,
snapshot store, collection loop, dual export, HTTP server, cancelable reload,
config primitives). Each vendor exporter is its own repo that imports the core,
implements a single `core.Source`, and calls one entry point. Because every
vendor emits the identical `license_` schema (guaranteed by the core's metric
constructors), N exporters land in one Prometheus and keep a single cross-vendor
Grafana / alerting view.

**Tech stack:** Go 1.26.x · prometheus/client_golang · OTLP (otel + otlpmetricgrpc)
· logrus · cobra · fsnotify · yaml. Per-vendor SDKs live only in their own repo.

---

## Global Constraints

- **Schema identity is inviolable.** Every vendor exporter emits the same
  `license_` metric names with the same label-key sets, produced *only* through
  the core's constructors. No vendor may mint a `Sample` by hand. This is the
  entire reason the split keeps a shared core rather than N independent repos.
- **Absent-not-zero, raw-facts** carry over unchanged (ADR-0004/0005): omit
  perpetual expiration, omit unlimited totals, never emit a fake `0`/sentinel.
- **Secrets** stay `${ENV}` / `passwordFile` / `clientSecretFile` only; `--trace`
  never enables SDK debug (token/cookie leak).
- **No behavior change to the wire format.** A scrape of the split m365 exporter
  must be byte-for-byte equivalent (modulo instance labels) to today's unified
  exporter with only m365 enabled. This is the migration's correctness oracle.
- **Family conformance** (per `exporter-standards`): each vendor repo carries the
  standard Makefile contract, CI trio, GoReleaser, docs, ADRs.

---

## Background & decision record

The unified `licenses_exporter` (shipped v1.0.0) put VMware + M365 in one binary.
Three forces motivate splitting per vendor:

1. **Dependency weight is real and compounding.** Measured: 594 packages compiled
   for the unified binary — `msgraph-sdk-go` (88) + `azure-sdk/azidentity` (29) ≈
   117 for M365, `govmomi` (23) for VMware, over a 270-package shared core
   (Prometheus + OTLP/otel/gRPC). Each new vendor (Veeam next) adds its SDK to a
   single ever-heavier binary.
2. **It already caused a real failure.** The v1.0.0 release OOM-killed the CI
   runner twice, building all four target arches of the heavy binary
   concurrently (fixed tactically with `--parallelism 1`). The lighter,
   hand-rolled family siblings never hit this.
3. **Family fit.** Every other exporter (`pflex`, `pmax`, `pstore`, …) is
   one-repo-per-vendor. The unified exporter is the outlier.

**Decisions taken** (this session):

- Split **per vendor company**, keeping a **shared `license_` schema**.
- Structure: **multi-repo + a shared `licenses-exporter-core` module** (not a
  monorepo — the VMware/Veeam repos must never compile `msgraph-sdk-go`).
- The current `licenses_exporter` repo **becomes `m365_licenses_exporter`**.
- Splitting *binaries* only; never the schema (split schema → lose the single-pane
  cross-vendor view, which is the product).

**Explicitly out of scope of this spec** (own sub-projects later):

- `vmware_licenses_exporter` (second consumer; promotes core to v1.0.0).
- `veeam_licenses_exporter` (new collector; needs a Veeam licensing-API research
  pass first).
- Replacing `msgraph-sdk-go` with a hand-rolled `resty` Graph client (a build-time
  win worth ~117 packages) — evaluated when standing up the m365 collector, but
  not required for the extraction to be correct.

---

## Module identity

- **Repo/module:** `github.com/fjacquet/licenses-exporter-core`
- **Package name:** `licenses_core` (imported as `core` by consumers).
- **Versioning:** starts at **`v0.1.0`** while the seam settles against the first
  consumer (m365). Promoted to **`v1.0.0`** only once a *second* consumer
  (vmware) compiles and runs against it unchanged — two independent consumers is
  the proof the boundary is right. Semantic-import versioning applies at v2+.

---

## The seam — public API surface

Everything below is exported by `core`; everything else in today's `internal/`
becomes internal to the module.

### Schema (moves from `internal/license/metrics.go`, unchanged)

```go
type Label struct{ Key, Value string }
type Sample struct {
    Name   string
    Value  float64
    Labels []Label
}

// Metric name constants (license_ prefix) — the single source of schema truth.
const (
    MetricSeatsTotal      = "license_seats_total"
    MetricSeatsUsed       = "license_seats_used"
    MetricExpiration      = "license_expiration_timestamp_seconds"
    MetricUp              = "license_up"
    MetricLastSuccess     = "license_collector_last_success_timestamp_seconds"
    MetricScrapeDuration  = "license_scrape_duration_seconds"
    MetricBuildInfo       = "license_build_info"
)

// Constructors — the ONLY way to build a Sample. Each stamps a fixed label-key
// set, which is what guarantees schema identity across every vendor.
func SeatSample(name, vendor, product, unit, instance string, v float64) Sample
func ExpirationSample(vendor, product, instance string, ts float64) Sample
// (health/build_info samples are emitted by the engine, not vendors)
```

### The vendor extension point (from `internal/license/source.go`)

```go
type Source interface {
    Vendor() string                          // constant per exporter, e.g. "microsoft"
    Instance() string                        // tenant / vCenter identifier
    Collect(ctx context.Context) ([]Sample, error)
}
```

A vendor implements `Source` (+ a `NewSources(cfg) ([]Source, error)` constructor)
and nothing else engine-related.

### Config primitives (from `internal/config`)

```go
// Base is the vendor-neutral config block. A vendor config embeds it inline.
type Base struct {
    Collection CollectionConfig `yaml:"collection"` // Interval (default 2h)
    OTLP       OTLPConfig       `yaml:"otlp"`        // Endpoint, Insecure (verbatim from source; push cadence is a const, not config)
}

func LoadYAML(path string, into any) error          // read + strict ${ENV} expand + unmarshal
func Expand(raw []byte) ([]byte, error)             // strict ${VAR} expansion, fail on unset
func ResolveSecret(inline, file string) (string, error)
func (b Base) Validate() error                       // interval > 0, otlp coherence
```

### The entry point (generalizes today's `main.go` + `internal/app`)

```go
// App is what a vendor main assembles. Load re-parses the vendor's whole config
// (base + vendor block) and rebuilds sources; the engine calls it at startup and
// on every reload, so vendor-specific config changes hot-reload too.
type App struct {
    Version    string
    Addr       string // --web.listen-address
    Once       bool   // --once
    Debug      bool   // gates the --once sample dump
    Trace      bool   // repo-owned transport tracing only (never SDK debug)
    ConfigPath string // --config; enables file-watch reload (empty => SIGHUP-only)
    Load       func() (Base, []Source, error)
}

// Main runs the whole lifecycle: --once path, or bind-once server + shared
// snapshot store + dual export + cancelable validated reload + signal handling.
// A vendor main is ~30 lines: parse flags, define Load, call core.Main.
func Main(app App) error
```

A reference vendor `main.go` (this repo, post-conversion):

```go
func main() {
    var cfgPath, addr string
    var once, debug, trace bool
    root := &cobra.Command{Use: "m365_licenses_exporter", RunE: func(*cobra.Command, []string) error {
        return core.Main(core.App{
            Version: version, Addr: addr, Once: once, Debug: debug, Trace: trace,
            Load: func() (core.Base, []core.Source, error) {
                var cfg Config // { core.Base `yaml:",inline"`; M365 M365Config `yaml:"m365"` }
                if err := core.LoadYAML(cfgPath, &cfg); err != nil { return core.Base{}, nil, err }
                if err := cfg.Base.Validate(); err != nil { return core.Base{}, nil, err }
                srcs, err := m365.NewSources(cfg.M365)
                return cfg.Base, srcs, err
            },
        })
    }}
    // flag wiring … ; root.Execute()
}
```

---

## What moves where (migration map)

| Today (in `licenses_exporter`) | Destination |
|---|---|
| `internal/license/*` (schema, snapshot, collector loop, prom collector, otlp) | **core** (mostly exported surface above; rest internal) |
| `internal/app/*` (Server, reload state machine, Health, RunOnce, dumpSamples) | **core** (becomes `core.Main` + internals) |
| `internal/config/*` (Expand, ResolveSecret, Load) | **core** as `Base` + primitives; vendor-block structs leave |
| `main.go` (serveWithReload, watcher, signal adapter) | **core.Main** internals; a thin vendor `main.go` remains |
| `internal/m365/*` | **stays** — this repo becomes `m365_licenses_exporter` |
| `internal/vmware/*` | **removed here**, re-homed in `vmware_licenses_exporter` (next sub-project) |
| `config/config.go` `Collectors.{M365,VMware}` composite | replaced by the vendor's own `Config{ core.Base; M365 … }` |
| label-parity test | **core** — locks each metric name's label-key set at the constructor level |

**This repo after conversion (`m365_licenses_exporter`):** `main.go` (thin) +
`internal/m365/` (collector + its config struct + `NewSources`) + a `Config` type
embedding `core.Base`. Imports `github.com/fjacquet/licenses-exporter-core`. Drops
all generic code and the VMware collector. `config.yaml` keeps only the
`collection:`, `otlp:`, and `m365:` blocks.

---

## Schema-identity guarantee

Today a runtime label-parity test compares label keys *across* vendors. Post-split
there is no single process holding all vendors, so the invariant shifts left:

- The **core constructors** are the sole `Sample` factories; each stamps a fixed,
  ordered label-key set per metric name.
- A **core unit test** locks those label-key sets (golden assertion): if anyone
  changes a constructor's labels, core's own test fails before any vendor ships.
- Each vendor test asserts its collector's output *through the core registry
  gather and an OTLP `ManualReader`*, so a vendor that bypasses a constructor is
  caught in its own CI.

Net: identity is guaranteed by construction + one core test, not by co-locating
vendors.

---

## Error handling (inherited, unchanged)

- Per-source failure → `license_up{vendor,instance}=0`; the cycle never crashes.
- Reload validates the candidate config *before* tearing down the running loop; an
  invalid reload is logged and the last-good snapshot keeps serving.
- Cold start serves `license_build_info` only until the first collect resolves.
- Retry excludes 4xx (never retry auth failures).

## Testing strategy

- **core**: full unit coverage of schema constructors (label-key golden test),
  snapshot store (pointer-swap, immutability), collection loop (degradation,
  single-initial-collect), Prometheus gather + OTLP `ManualReader` parity, and the
  reload state machine (the existing gated-source continuity test moves here).
  A `fakeSource` stands in for a real vendor.
- **m365 (this repo)**: collector tests (mock Graph transport) asserting samples
  via core's gather + `ManualReader`; a golden scrape compared against the current
  unified exporter's m365-only output (the migration correctness oracle).

## Non-goals / YAGNI

- No schema changes; no new metrics; no new vendors in this sub-project.
- No msgraph→resty rewrite here (separate, optional, m365-scoped follow-up).
- No monorepo; no config format redesign beyond the base/vendor split.

## Open decisions (resolve during planning)

1. **Package name** `licenses_core` vs `core` vs `licensecore` — cosmetic; pick one.
2. **`Addr` as flag vs config** — keep `--web.listen-address` as a flag (today's
   behavior) or fold into `Base`? Proposal: keep it a flag, passed via `App.Addr`.
3. **Repo rename mechanics** — renaming `licenses_exporter` → `m365_licenses_exporter`
   changes the module path (a v2-scale break). Confirm the rename + module path,
   and whether v1.0.0 (unified) stays as this repo's final unified tag before the
   m365 line starts fresh.
4. **Extraction order** — publish core `v0.1.0` from a fresh repo first, then point
   this repo at it; or develop both together via a `replace` directive until the
   seam stabilizes, then cut `v0.1.0`. Proposal: `replace` during development.
