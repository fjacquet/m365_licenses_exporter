# licenses-exporter-core Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extract the vendor-neutral engine of `licenses_exporter` into a standalone, importable Go module `github.com/fjacquet/licenses-exporter-core` that publishes `v0.1.0`.

**Architecture:** This is an **extraction/refactor**, not greenfield: the packages `internal/license`, `internal/app`, and `internal/config` already exist, are tested, and pass. Each task moves one cohesive package into the new module, promotes exactly the API named in the spec's seam to exported symbols, ports that package's existing tests as the safety net, and verifies green. A test-only `fakeSource` stands in for a real vendor so the engine is exercised end-to-end without any vendor SDK. The deliverable is a library with `core.Source` as the extension seam and `core.Main` as the single entry point.

**Tech Stack:** Go 1.26.4 · prometheus/client_golang · go.opentelemetry.io/otel + otlpmetricgrpc · sirupsen/logrus · spf13/cobra (consumers only) · fsnotify · gopkg.in/yaml.v3.

## Global Constraints

- **Package + module:** package name `licenses_core`; module path `github.com/fjacquet/licenses-exporter-core`; consumers import it aliased as `core`.
- **Library, not a binary:** no `cmd/`, no `main` package, no GoReleaser/Docker/Homebrew/cli target. CI is `lint + test + vuln` only.
- **Schema identity is inviolable:** every `Sample` is built *only* by a core constructor; a core golden test locks the vendor-facing constructors' label-key sets (seats + expiration), with the remaining engine-emitted metric names extended in a later release (see the core CHANGELOG's deferred items).
- **Absent-not-zero, raw-facts:** omit perpetual expiration and unlimited totals; never emit a fake `0`/sentinel; an unparseable value yields an absent sample.
- **Secrets:** `${ENV}` / file-based only; `Expand` fails on unset `${VAR}`; `--trace` (consumer flag) never enables SDK debug.
- **Behaviour parity:** moved code keeps identical behaviour — the ported tests must pass unchanged except for import-path/package edits.
- **Go 1.26.4**, family CI via `fjacquet/ci@v1` reusable workflows, `# nosemgrep` retained on caller `uses:` lines (family standard).
- **No secrets/tokens** in code or logs; scan generated code with semgrep before commit; commit trailer required.

---

## Target file structure (the module)

```
licenses-exporter-core/                 module github.com/fjacquet/licenses-exporter-core
  go.mod / go.sum                       package licenses_core, go 1.26.4
  doc.go                                package doc comment
  sample.go        Sample, Label                             (from internal/license/sample.go)
  metrics.go       metric-name consts + Sample constructors  (from internal/license/metrics.go)
  source.go        Source interface                          (from internal/license/source.go)
  snapshot.go      Snapshot, SnapshotStore, ColdStartSnapshot(from internal/license/snapshot.go)
  collector.go     Collector + CollectOnce/RunTicker/Run     (from internal/license/collector.go)
  prometheus.go    NewPromCollector (unchecked collector)    (from internal/license/prometheus.go)
  otlp.go          RegisterOTLP + setupOTLP                  (from internal/license/otlp.go + internal/app/otlp.go)
  config.go        Base, Expand, ResolveSecret, LoadYAML     (from internal/config/config.go)
  dotenv.go        LoadDotEnv                                (from internal/config/dotenv.go)
  health.go        Health                                    (from internal/app/health.go)
  server.go        Server, NewServer, RunCollection, ReloadLoop, Shutdown (from internal/app/app.go)
  run.go           App, Main, RunOnce, dumpSamples, signal/watcher adapter (from main.go + app.go)
  fake_source_test.go   test-only fakeSource + gatedSource helpers
  *_test.go        ported tests
  Makefile, .golangci.yml, .github/workflows/{ci,security}.yml, dependabot.yml, LICENSE, README.md
```

Package-level renames when a symbol becomes exported API (everything else moves verbatim):
`license.X → licenses_core.X` (already exported), `config.Config → (dropped; replaced by Base)`, `app.Server → licenses_core.Server`, `app.RunOnce → licenses_core.RunOnce`.

---

### Task 1: Scaffold the core module

**Files:**

- Create: `go.mod`, `doc.go`, `Makefile`, `.golangci.yml`, `.github/workflows/ci.yml`, `.github/workflows/security.yml`, `.github/dependabot.yml`, `LICENSE`, `README.md`, `.gitignore`
- Test: `scaffold_test.go`

**Interfaces:**

- Consumes: nothing.
- Produces: an importable module `github.com/fjacquet/licenses-exporter-core`, package `licenses_core`.

- [ ] **Step 1: Write the failing test**

`scaffold_test.go`:

```go
package licenses_core

import "testing"

func TestModuleBuilds(t *testing.T) {
 // Placeholder proving the package compiles + test harness runs; replaced by
 // real tests in later tasks. Kept trivial on purpose.
 if want := "license_"; want == "" {
  t.Fatal("unreachable")
 }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./...`
Expected: FAIL — `go.mod`/package not yet present (`go: cannot find main module`).

- [ ] **Step 3: Create the module scaffold**

`go.mod`:

```
module github.com/fjacquet/licenses-exporter-core

go 1.26.4
```

`doc.go`:

```go
// Package licenses_core is the vendor-neutral engine for the licenses_exporter
// family: the license_ metric schema and its constructors, an immutable snapshot
// store, the collection loop, the Prometheus + OTLP export paths, and the HTTP
// server with cancelable validated hot reload. Vendor exporters implement Source
// and call Main.
package licenses_core
```

`Makefile` — copy the family Makefile but **drop** the `cli`, `docker`, `run-cli`, `release`, `release-snapshot`, `sbom` targets (this is a library). Keep: `tools fmt-check fmt vet lint test test-race test-coverage vuln ci sure clean`. `ci: lint test build vuln` where `build` is `go build ./...`.

`.golangci.yml`, `.github/workflows/ci.yml` (caller of `fjacquet/ci/.github/workflows/go-ci.yml@v1` with the `# nosemgrep` line), `.github/workflows/security.yml`, `.github/dependabot.yml`, `LICENSE` (Apache-2.0), `README.md`, `.gitignore` — copy from `licenses_exporter` verbatim (drop release/docs jobs the library doesn't need).

- [ ] **Step 4: Run test to verify it passes**

Run: `make ci`
Expected: gofmt clean, `golangci-lint` 0 issues, `go test ./...` PASS, `go build ./...` clean, govulncheck "No vulnerabilities found".

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "chore: scaffold licenses-exporter-core module (library CI, no binary)"
```

---

### Task 2: Port the schema — samples, labels, metric constructors

**Files:**

- Create: `sample.go`, `metrics.go`, `metrics_test.go`
- Remove (from source repo, done in Plan 2): `internal/license/sample.go`, `internal/license/metrics.go`

**Interfaces:**

- Consumes: nothing.
- Produces:

  ```go
  type Label struct{ Key, Value string }
  type Sample struct{ Name string; Value float64; Labels []Label }
  const ( MetricSeatsTotal="license_seats_total"; MetricSeatsUsed="license_seats_used"
          MetricExpiration="license_expiration_timestamp_seconds"; MetricUp="license_up"
          MetricLastSuccess="license_collector_last_success_timestamp_seconds"
          MetricScrapeDuration="license_scrape_duration_seconds"; MetricBuildInfo="license_build_info" )
  func SeatSample(name, vendor, product, unit, instance string, v float64) Sample
  func ExpirationSample(vendor, product, instance string, ts float64) Sample
  // plus the health/build_info sample builders used by the engine
  ```

- [ ] **Step 1: Write the failing test**

Port `internal/license/metrics_test.go` into `metrics_test.go` (change `package license` → `package licenses_core`, drop the `license.` qualifier). Then ADD the schema-identity golden test that locks label keys:

```go
func TestMetricLabelKeysAreLocked(t *testing.T) {
 // Guards schema identity across every vendor: if a constructor's label set
 // changes, this fails before any vendor ships.
 cases := []struct {
  name   string
  sample Sample
  want   []string
 }{
  {"seats", SeatSample(MetricSeatsTotal, "microsoft", "SPE_E5", "users", "t-a", 1), []string{"vendor", "product", "unit", "instance"}},
  {"exp", ExpirationSample("microsoft", "SPE_E5", "t-a", 1), []string{"vendor", "product", "instance"}},
 }
 for _, c := range cases {
  var got []string
  for _, l := range c.sample.Labels {
   got = append(got, l.Key)
  }
  if len(got) != len(c.want) {
   t.Fatalf("%s: label keys %v, want %v", c.name, got, c.want)
  }
  for i := range c.want {
   if got[i] != c.want[i] {
    t.Fatalf("%s: label[%d]=%q want %q", c.name, i, got[i], c.want[i])
   }
  }
 }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./... -run TestMetric`
Expected: FAIL — `undefined: Sample`, `undefined: SeatSample`.

- [ ] **Step 3: Move the implementation**

Move `internal/license/sample.go` → `sample.go` and `internal/license/metrics.go` → `metrics.go`; change `package license` → `package licenses_core`. No logic changes.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./... -run 'TestMetric|TestSeat|TestExpiration'`
Expected: PASS (ported tests + the new golden test).

- [ ] **Step 5: Commit**

```bash
git add sample.go metrics.go metrics_test.go
git commit -m "feat: license_ schema + constructors with label-key golden test"
```

---

### Task 3: Port the Source seam + snapshot store, add fakeSource

**Files:**

- Create: `source.go`, `snapshot.go`, `snapshot_test.go`, `fake_source_test.go`

**Interfaces:**

- Consumes: `Sample` (Task 2).
- Produces:

  ```go
  type Source interface {
      Vendor() string
      Instance() string
      Collect(ctx context.Context) ([]Sample, error)
  }
  type Snapshot struct { /* Samples []Sample + metadata, unchanged */ }
  func ColdStartSnapshot(version, goversion string) *Snapshot
  type SnapshotStore struct { /* unchanged */ }
  func NewSnapshotStore(initial *Snapshot) *SnapshotStore
  func (s *SnapshotStore) Load() *Snapshot
  func (s *SnapshotStore) Store(*Snapshot)
  ```

- [ ] **Step 1: Write the failing test**

Add `fake_source_test.go`:

```go
package licenses_core

import "context"

// fakeSource is a deterministic Source for engine tests — no vendor SDK.
type fakeSource struct {
 vendor, instance string
 samples          []Sample
 err              error
}

func (f *fakeSource) Vendor() string   { return f.vendor }
func (f *fakeSource) Instance() string { return f.instance }
func (f *fakeSource) Collect(context.Context) ([]Sample, error) {
 return f.samples, f.err
}
```

Port `internal/license/snapshot_test.go` → `snapshot_test.go` (package + qualifier edits).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./... -run TestSnapshot`
Expected: FAIL — `undefined: NewSnapshotStore`, `undefined: Source`.

- [ ] **Step 3: Move the implementation**

Move `internal/license/source.go` → `source.go` and `internal/license/snapshot.go` → `snapshot.go`; `package license` → `package licenses_core`. No logic changes.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./... -run TestSnapshot -race`
Expected: PASS (pointer-swap + immutability assertions hold).

- [ ] **Step 5: Commit**

```bash
git add source.go snapshot.go snapshot_test.go fake_source_test.go
git commit -m "feat: Source seam + immutable SnapshotStore + test fakeSource"
```

---

### Task 4: Port the collection loop

**Files:**

- Create: `collector.go`, `collector_test.go`

**Interfaces:**

- Consumes: `Source`, `SnapshotStore`, `Sample` (Tasks 2–3).
- Produces:

  ```go
  type Collector struct { /* unchanged */ }
  func NewCollector(sources []Source, store *SnapshotStore, version, goversion string, timeout time.Duration, now func() time.Time) *Collector
  func (c *Collector) CollectOnce(ctx context.Context) *Snapshot
  func (c *Collector) RunTicker(ctx context.Context, interval time.Duration)
  func (c *Collector) Run(ctx context.Context, interval time.Duration)
  ```

- [ ] **Step 1: Write the failing test**

Port `internal/license/collector_test.go` → `collector_test.go`. Its fakes are replaced by `fakeSource` from Task 3 (delete any local duplicate). Ensure it still covers: a failing source degrades to `license_up{vendor,instance}=0` without aborting the cycle; `CollectOnce` then `RunTicker` yields exactly one leading collect (no double initial collect).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./... -run TestCollect`
Expected: FAIL — `undefined: NewCollector`.

- [ ] **Step 3: Move the implementation**

Move `internal/license/collector.go` → `collector.go`; `package license` → `package licenses_core`. No logic changes.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./... -run TestCollect -race`
Expected: PASS (degradation + single-initial-collect assertions hold).

- [ ] **Step 5: Commit**

```bash
git add collector.go collector_test.go
git commit -m "feat: errgroup collection loop with per-source degradation"
```

---

### Task 5: Port the Prometheus unchecked collector

**Files:**

- Create: `prometheus.go`, `prometheus_test.go`

**Interfaces:**

- Consumes: `SnapshotStore`, `Sample` (Tasks 2–3).
- Produces: `func NewPromCollector(store *SnapshotStore) prometheus.Collector`.

- [ ] **Step 1: Write the failing test**

Port `internal/license/prometheus_test.go` → `prometheus_test.go`. It builds a store with a known snapshot, registers `NewPromCollector`, gathers via a `prometheus.NewRegistry()`, and asserts the exposed families/labels equal the snapshot's samples.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./... -run TestProm`
Expected: FAIL — `undefined: NewPromCollector`.

- [ ] **Step 3: Move the implementation**

Move `internal/license/prometheus.go` → `prometheus.go`; `package license` → `package licenses_core`. No logic changes.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./... -run TestProm`
Expected: PASS (registry gather equals snapshot).

- [ ] **Step 5: Commit**

```bash
git add prometheus.go prometheus_test.go
git commit -m "feat: unchecked Prometheus collector over the snapshot"
```

---

### Task 6: Port the OTLP export path

**Files:**

- Create: `otlp.go`, `otlp_test.go`

**Interfaces:**

- Consumes: `SnapshotStore`, `OTLPConfig` (defined here; Base wires it in Task 7).
- Produces:

  ```go
  type OTLPConfig struct { Endpoint string; Insecure bool /* verbatim from source config.OTLPConfig */ }
  // NOTE: source config.OTLPConfig has ONLY these two fields; the push cadence is a
  // hardcoded `const otlpPushInterval = 60*time.Second` in otlp.go, NOT a config field.
  // (An earlier draft sketched Headers/PushInterval — dropped in T6 integration to hold
  // behaviour-parity. Task 7's Base embeds this 2-field struct.)
  func setupOTLP(ctx context.Context, cfg OTLPConfig, version, instanceID string, store *SnapshotStore) (shutdown func(context.Context) error, err error)
  // RegisterOTLP registers the observable gauges against a meter/reader.
  ```

- [ ] **Step 1: Write the failing test**

Merge `internal/license/otlp_test.go` and `internal/app/otlp_test.go` into `otlp_test.go`. Assert via an OTLP `metric.NewManualReader()` that a known snapshot produces the same metric points as the Prometheus path (dual-export parity), gated so no exporter is created when `Endpoint == ""`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./... -run TestOTLP`
Expected: FAIL — `undefined: setupOTLP`.

- [ ] **Step 3: Move the implementation**

Move `internal/license/otlp.go` and the OTLP wiring from `internal/app/otlp.go` into `otlp.go`; `package license`/`app` → `package licenses_core`. Keep the `otlp.endpoint`-empty → no exporter gate. No logic changes.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./... -run TestOTLP -race`
Expected: PASS (ManualReader parity; gated on endpoint).

- [ ] **Step 5: Commit**

```bash
git add otlp.go otlp_test.go
git commit -m "feat: OTLP observable-gauge export (gated on endpoint)"
```

---

### Task 7: Port config primitives and define Base

**Files:**

- Create: `config.go`, `dotenv.go`, `config_test.go`

**Interfaces:**

- Consumes: `OTLPConfig` (Task 6).
- Produces:

  ```go
  type CollectionConfig struct { Interval time.Duration `yaml:"interval"` }
  type Base struct {
      Collection CollectionConfig `yaml:"collection"`
      OTLP       OTLPConfig       `yaml:"otlp"`
  }
  func (b Base) Validate() error                 // Interval > 0; OTLP coherence
  func LoadYAML(path string, into any) error     // read + strict ${ENV} expand + yaml.Unmarshal
  func Expand(raw []byte) ([]byte, error)        // strict ${VAR}; error on unset
  func ResolveSecret(inline, file string) (string, error)
  func LoadDotEnv(path string) error             // never overrides real env
  ```

- [ ] **Step 1: Write the failing test**

Port the relevant cases from `internal/config/config_test.go` → `config_test.go`: `Expand` fails on unset `${VAR}`; `ResolveSecret` prefers inline, falls back to file, errors if both/neither per current rules; `LoadYAML` round-trips a `collection`/`otlp` doc into `Base`; `Base.Validate` rejects `Interval <= 0`. Keep the dotenv test (`LoadDotEnv` does not override an already-set env var).

Note: **drop** the old `Config`/`Collectors.{M365,VMware}` struct and its "at least one collector enabled" validation — that logic belongs to each vendor's config in Plan 2, not core.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./... -run 'TestExpand|TestResolveSecret|TestLoadYAML|TestBaseValidate|TestDotEnv'`
Expected: FAIL — `undefined: Expand`, `undefined: Base`.

- [ ] **Step 3: Move the implementation**

Move `internal/config/config.go`'s `Expand`, `ResolveSecret` and add `LoadYAML` (generic: read file → `Expand` → `yaml.Unmarshal(into)`); define `Base`, `CollectionConfig`, `Base.Validate` (interval default/validation lifted from the old `validate`). Move `internal/config/dotenv.go` → `dotenv.go`. `package config` → `package licenses_core`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./... -run 'TestExpand|TestResolveSecret|TestLoadYAML|TestBaseValidate|TestDotEnv'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add config.go dotenv.go config_test.go
git commit -m "feat: config primitives + Base (collection/otlp) + LoadYAML"
```

---

### Task 8: Port health + the server and reload state machine

**Files:**

- Create: `health.go`, `server.go`, `health_test.go`, `server_test.go` (ported reload/app tests)

**Interfaces:**

- Consumes: `SnapshotStore`, `Collector`, `Source`, `Base`, `OTLPConfig` (Tasks 3–7).
- Produces:

  ```go
  type Health struct { /* unchanged */ }           // ServeHTTP + SetReady
  type Server struct { /* unchanged fields */ }
  func NewServer(base Base, version, addr string, buildSources func() ([]Source, error)) (*Server, error)
  func (s *Server) RunCollection(ctx context.Context, interval time.Duration) error
  func (s *Server) ReloadLoop(initialInterval time.Duration, reloads, shutdown <-chan struct{}, load func() (Base, []Source, error))
  func (s *Server) Shutdown(ctx context.Context) error
  ```

  Change from the current `app.Server`: `NewServer` takes `Base` + a `buildSources func() ([]Source, error)` (no vendor config type); `RunCollection` takes the interval directly; `ReloadLoop`'s `load` returns `(Base, []Source, error)`.

- [ ] **Step 1: Write the failing test**

Port `internal/app/health_test.go` → `health_test.go`. Port `internal/app/reload_test.go` (the `gatedSource` continuity test — never-blanks-mid-reload, reject-bad-reload keeps last-good, health stays ready, shutdown returns) and the relevant `internal/app/app_test.go` cases → `server_test.go`, adapting to the new `NewServer`/`ReloadLoop` signatures. Move `gatedSource` into `fake_source_test.go`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./... -run 'TestReload|TestServer|TestHealth'`
Expected: FAIL — `undefined: NewServer` / signature mismatch.

- [ ] **Step 3: Move the implementation**

Move `internal/app/health.go` → `health.go`. Move `internal/app/app.go`'s `Server`, `NewServer`, `RunCollection`, `ReloadLoop`, `Shutdown` → `server.go`, applying the signature changes above; the shared-store reload semantics (build server/store/OTLP once, reload swaps only the collector) are unchanged. Drop `app.BuildSources` and `app.RunOnce` here (RunOnce moves to Task 9). `package app` → `package licenses_core`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./... -run 'TestReload|TestServer|TestHealth' -count=50 -race`
Expected: PASS (reload continuity holds deterministically under race).

- [ ] **Step 5: Commit**

```bash
git add health.go server.go health_test.go server_test.go fake_source_test.go
git commit -m "feat: HTTP server + shared-store cancelable reload state machine"
```

---

### Task 9: Define the entry point — App, Main, RunOnce

**Files:**

- Create: `run.go`, `run_test.go`

**Interfaces:**

- Consumes: everything above.
- Produces:

  ```go
  type App struct {
      Version string
      Addr    string
      Once    bool
      Debug   bool
      Trace   bool
      Load    func() (Base, []Source, error)   // re-parsed at startup and on each reload
  }
  func Main(app App) error
  func RunOnce(ctx context.Context, base Base, sources []Source, version string, debug bool) error
  ```

- [ ] **Step 1: Write the failing test**

`run_test.go`:

```go
func TestMainOnceDumpsWithFakeSource(t *testing.T) {
 src := &fakeSource{vendor: "acme", instance: "i1",
  samples: []Sample{SeatSample(MetricSeatsUsed, "acme", "p", "u", "i1", 3)}}
 app := App{Version: "test", Once: true, Debug: false,
  Load: func() (Base, []Source, error) {
   return Base{Collection: CollectionConfig{Interval: time.Hour}}, []Source{src}, nil
  }}
 if err := Main(app); err != nil {
  t.Fatalf("Main --once returned error: %v", err)
 }
}

func TestMainServesAndReloads(t *testing.T) {
 // Bind :0, hit /health and /metrics, trigger one reload via the exported
 // hook, confirm /metrics still serves the prior snapshot throughout, then
 // shut down cleanly. (Reuse the gatedSource from fake_source_test.go.)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./... -run TestMain`
Expected: FAIL — `undefined: App`, `undefined: Main`.

- [ ] **Step 3: Move the implementation**

Create `run.go`: move `app.RunOnce` (dump gated on `Debug`) and `dumpSamples` from `internal/app/app.go`, and the `serveWithReload` body + `watcherEvents`/`watcherErrors`/signal adapter from `main.go`. `Main(app)`:

1. `base, sources, err := app.Load()`; on error return it (fatal at startup).
2. if `app.Once`: `return RunOnce(ctx, base, sources, app.Version, app.Debug)`.
3. else: `NewServer(base, app.Version, app.Addr, func() ([]Source, error){ ... })`, wire signals + fsnotify into the coalescing `reloads`/`shutdown` channels, and drive `ReloadLoop(base.Collection.Interval, reloads, shutdown, app.Load)`. The config file path for the watcher comes from the consumer via a small `App` addition if needed — keep the watcher optional (SIGHUP always works), matching current behaviour.

Note: cobra/flag parsing stays in the **consumer**; `Main` takes the already-parsed `App`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./... -run TestMain -race`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add run.go run_test.go
git commit -m "feat: core.App + core.Main entry point (once/serve/reload)"
```

---

### Task 10: Green gate + publish v0.1.0

**Files:**

- Modify: `README.md` (usage: the ~30-line consumer `main.go` example from the spec), `go.mod`/`go.sum` (tidy)

**Interfaces:**

- Consumes: the whole module.
- Produces: a published module version `v0.1.0`.

- [ ] **Step 1: Tidy + full gate**

Run: `go mod tidy && make ci`
Expected: gofmt clean, `golangci-lint` 0 issues, `go test ./... -race` PASS, `go build ./...` clean, govulncheck clean.

- [ ] **Step 2: Semgrep scan**

Run: `uvx semgrep scan --config auto --skip-unknown-extensions .`
Expected: 0 blocking findings.

- [ ] **Step 3: Document the consumer contract**

Add to `README.md` the worked consumer example (thin vendor `main.go` calling `core.Main`, and a `Config` embedding `core.Base`), copied from the design spec's "reference vendor main.go" section.

- [ ] **Step 4: Commit + tag**

```bash
git add -A
git commit -m "docs: consumer contract + tidy for v0.1.0"
git tag -a v0.1.0 -m "v0.1.0 — extracted core engine (API settling; promoted to v1.0.0 after a 2nd consumer)"
git push origin main --tags
```

Expected: `go list -m github.com/fjacquet/licenses-exporter-core@v0.1.0` resolves.

- [ ] **Step 5: Verify importability**

In a scratch dir: `go mod init tmp && go get github.com/fjacquet/licenses-exporter-core@v0.1.0 && echo 'package main; import _ "github.com/fjacquet/licenses-exporter-core"; func main(){}' > m.go && go build .`
Expected: builds clean.

---

## Notes for the follow-on (Plan 2: m365_licenses_exporter)

Not part of this plan; recorded so the seam stays honest:

- This repo (`licenses_exporter`) adds `require github.com/fjacquet/licenses-exporter-core v0.1.0`, deletes `internal/license`, `internal/app`, `internal/config`, `internal/vmware`, and `main.go`'s engine body; keeps `internal/m365` and a thin `main.go` calling `core.Main`.
- Define `type Config struct { core.Base yaml:",inline"; M365 M365Config yaml:"m365" }`; `m365.NewSources` returns `[]core.Source`.
- **Correctness oracle:** a golden `--once --debug` scrape must match today's unified exporter run with only m365 enabled (modulo instance labels).
- Resolve the deferred repo-rename + module-path decision (`licenses_exporter` → `m365_licenses_exporter`) before starting.
- `vmware_licenses_exporter` (Plan 3) then ports `internal/vmware` onto core and promotes core to `v1.0.0`; `veeam_licenses_exporter` (Plan 4) needs a Veeam licensing-API research pass first.

```

---

## Self-Review

**Spec coverage:** every core-owned component in the spec's migration map has a task — schema (T2), Source+snapshot (T3), collection loop (T4), Prometheus (T5), OTLP (T6), config primitives/Base (T7), server+reload (T8), core.Main entry point (T9), scaffold (T1), publish (T10). Schema-identity guarantee → T2 golden test. Testing strategy (fakeSource, ManualReader parity, gated-source continuity) → T3/T6/T8. The m365 conversion is explicitly deferred to Plan 2 (scope check).

**Placeholder scan:** no TBD/TODO; each task names exact files, exact signatures, and runnable commands with expected output. Extraction steps reference the precise source file per move rather than re-transcribing unchanged bodies (the code exists and is tested).

**Type consistency:** `Source`, `Sample`, `SnapshotStore`, `Base`, `Server`, `App`, `Main`, `NewServer`, `RunCollection`, `ReloadLoop`, `RunOnce` are used with identical signatures across tasks; `NewServer`/`ReloadLoop`/`RunCollection` signature changes vs the current `internal/app` are stated once (T8) and matched in T9.
