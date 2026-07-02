# licenses_exporter v1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a unified enterprise-license Prometheus + OTLP exporter whose first two collectors (Microsoft 365 via `msgraph-sdk-go`, VMware vSphere via `govmomi`) publish a generic `license_*` schema through the family snapshot model.

**Architecture:** A background loop fans out over per-target `Source`s (`errgroup`), builds one immutable `Snapshot`, and swaps it into a `SnapshotStore` (RWMutex). A Prometheus *unchecked* collector and OTLP observable gauges both read the latest snapshot — backend API load is decoupled from scrape/push cadence. Each vendor lives in its own package; the pure parse function is split from the SDK fetch so both collectors are unit-testable without live infrastructure.

**Tech Stack:** Go 1.26.4, `prometheus/client_golang`, `go.opentelemetry.io/otel` (+ otlpmetricgrpc), `vmware/govmomi` (+ vcsim), `microsoftgraph/msgraph-sdk-go` (+ `azidentity`), `spf13/cobra`, `gopkg.in/yaml.v2`, `sirupsen/logrus`, `golang.org/x/sync/errgroup`, `fsnotify/fsnotify`, `joho/godotenv`.

**Reference docs:**

- Design spec: `docs/superpowers/specs/2026-07-01-licenses-exporter-design.md`
- Review addendum: `docs/superpowers/specs/2026-07-01-licenses-exporter-review.md`
- Repo guidance: `CLAUDE.md`
- Sibling repos to copy family boilerplate from: `/Users/fjacquet/Projects/pflex_exporter`, `/Users/fjacquet/Projects/ppdd_exporter`, `/Users/fjacquet/Projects/pstore_exporter`.

## Global Constraints

- **Go `1.26.4`** — pinned exactly in `go.mod` (`go 1.26.4`, not bare `go 1.26`).
- **CGO off** for release builds (`CGO_ENABLED=0`).
- **Metric prefix `license_`** on every emitted metric; identity labels only (`vendor,product,unit,instance`).
- **Absent, never zero/sentinel.** An unparseable/unlimited value yields an *absent* sample, never `0` or a magic number. Perpetual/unlimited → omit the series.
- **No exporter-computed policy.** No `days_to_expiration`, no `compliance_status` — expose raw facts only.
- **Label-key invariant.** Every series of a given metric name carries the same label-key set (see Task 4 parity test).
- **Retry excludes 4xx.** Never retry auth failures.
- **`--trace` wraps only repo-owned transports.** Never enable govmomi/msgraph SDK debug modes (they leak the token).
- **config.yaml is the source of truth.** Collector toggling is `enabled:` per collector; `${ENV_VAR}` expands in host/user/secret with fail-fast on unset; `passwordFile`/`clientSecretFile` for secrets.
- **Semgrep clean** — no inline `// nosemgrep` / `//nolint` suppressions; restructure instead.
- **Non-root Docker `USER`** is mandatory.
- **Metrics port `9105`.**
- **TDD + frequent commits.** Every task: failing test → run (fail) → minimal impl → run (pass) → commit. Assert collector behavior via **both** the Prometheus registry gather and an OTLP `ManualReader`.
- **Module path** `github.com/fjacquet/licenses_exporter`.

---

## File Structure

```
go.mod / go.sum
main.go                              # cobra CLI, HTTP server, wiring (Task 9)
internal/license/
  sample.go                          # Sample, Label, metric-name constants (Task 1)
  metrics.go                         # sample constructors enforcing per-metric label sets (Task 1)
  snapshot.go                        # Snapshot, SnapshotStore, ColdStartSnapshot (Task 2)
  source.go                          # Source interface (Task 3)
  collector.go                       # collection loop, per-source health, cold start (Task 3)
  prometheus.go                      # unchecked prometheus.Collector (Task 4)
  otlp.go                            # observable gauges + resource attrs (Task 5)
internal/config/
  config.go                          # YAML types, env expansion, validation (Task 6)
  dotenv.go                          # godotenv load-before-interpolation (Task 6)
internal/vmware/
  vmware.go                          # VMwareConfig, NewSources (Task 7)
  source.go                          # stateless login→list→logout Source (Task 7)
  parse.go                           # licensesToSamples (pure) (Task 7)
internal/m365/
  m365.go                            # M365Config, NewSources (Task 8)
  source.go                          # Source over a skuLister seam (Task 8)
  graph.go                           # graphSkuLister: msgraph + PageIterator (Task 8)
  parse.go                           # skusToSamples (pure) (Task 8)
config.yaml                          # sample config (Task 6)
Makefile / Dockerfile / Dockerfile.goreleaser / .goreleaser.yaml   # (Task 10)
.github/workflows/*.yml / .github/dependabot.yml                    # (Task 11)
docker-compose.yml / docker-compose.ghcr.yml / prometheus.yml
deploy/prometheus/license.rules.yml / grafana/**                   # (Task 12)
mkdocs.yml / docs/metrics.md / docs/adr/**                         # (Task 13)
```

---

## Task 1: Core sample types & metric vocabulary

**Files:**

- Create: `go.mod`, `internal/license/sample.go`, `internal/license/metrics.go`
- Test: `internal/license/metrics_test.go`

**Interfaces:**

- Produces:
  - `type Label struct { Key, Value string }`
  - `type Sample struct { Name string; Labels []Label; Value float64 }`
  - Metric-name consts: `MetricSeatsTotal`, `MetricSeatsUsed`, `MetricExpiration`, `MetricUp`, `MetricLastSuccess`, `MetricScrapeDuration`, `MetricBuildInfo` (all `license_*`).
  - `func SeatSample(name, vendor, product, unit, instance string, v float64) Sample` — name ∈ {SeatsTotal, SeatsUsed}, labels sorted `instance,product,unit,vendor`.
  - `func ExpirationSample(vendor, product, instance string, tsUnix float64) Sample` — labels `instance,product,vendor`.
  - `func UpSample(vendor, instance string, up bool) Sample`; `func LastSuccessSample(vendor, instance string, tsUnix float64) Sample`; `func ScrapeDurationSample(vendor, instance string, seconds float64) Sample` — labels `instance,vendor`.
  - `func BuildInfoSample(version, goVersion string) Sample` — labels `goversion,version`.

- [ ] **Step 1: Initialize the module**

Run:

```bash
cd /Users/fjacquet/Projects/licenses_exporter
go mod init github.com/fjacquet/licenses_exporter
```

Then edit `go.mod` so the version line reads exactly `go 1.26.4`.

- [ ] **Step 2: Write the failing test**

Create `internal/license/metrics_test.go`:

```go
package license

import "testing"

func labelValue(s Sample, key string) (string, bool) {
 for _, l := range s.Labels {
  if l.Key == key {
   return l.Value, true
  }
 }
 return "", false
}

func TestSeatSampleHasCanonicalLabelKeys(t *testing.T) {
 s := SeatSample(MetricSeatsTotal, "vmware", "vSphere_ENT+", "cpuPackage", "vcsa01", 512)
 if s.Name != "license_seats_total" {
  t.Fatalf("name = %q", s.Name)
 }
 if s.Value != 512 {
  t.Fatalf("value = %v", s.Value)
 }
 // Labels must be sorted by key: instance, product, unit, vendor.
 wantKeys := []string{"instance", "product", "unit", "vendor"}
 if len(s.Labels) != len(wantKeys) {
  t.Fatalf("label count = %d, want %d", len(s.Labels), len(wantKeys))
 }
 for i, k := range wantKeys {
  if s.Labels[i].Key != k {
   t.Fatalf("label[%d].Key = %q, want %q", i, s.Labels[i].Key, k)
  }
 }
 if v, _ := labelValue(s, "vendor"); v != "vmware" {
  t.Fatalf("vendor = %q", v)
 }
}

func TestUpSampleUsesVendorInstanceOnly(t *testing.T) {
 s := UpSample("microsoft", "tenant-a", false)
 if s.Name != "license_up" || s.Value != 0 {
  t.Fatalf("got %q=%v", s.Name, s.Value)
 }
 if len(s.Labels) != 2 {
  t.Fatalf("up label count = %d, want 2", len(s.Labels))
 }
 if _, ok := labelValue(s, "product"); ok {
  t.Fatal("up must not carry a product label")
 }
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/license/ -run TestSeatSample -v`
Expected: FAIL — build error, `SeatSample` / `MetricSeatsTotal` undefined.

- [ ] **Step 4: Write minimal implementation**

Create `internal/license/sample.go`:

```go
// Package license holds the vendor-agnostic sample model, snapshot store, and
// the two export paths (Prometheus + OTLP) that read snapshots.
package license

// Label is a single Prometheus label key/value pair.
type Label struct {
 Key   string
 Value string
}

// Sample is one vendor-agnostic metric point. Name is the full metric name
// (already prefixed with license_); Labels are in canonical (sorted-by-key)
// order for its metric name.
type Sample struct {
 Name   string
 Labels []Label
 Value  float64
}

// Metric names. Every metric is prefixed license_ (design spec §4).
const (
 MetricSeatsTotal     = "license_seats_total"
 MetricSeatsUsed      = "license_seats_used"
 MetricExpiration     = "license_expiration_timestamp_seconds"
 MetricUp             = "license_up"
 MetricLastSuccess    = "license_collector_last_success_timestamp_seconds"
 MetricScrapeDuration = "license_scrape_duration_seconds"
 MetricBuildInfo      = "license_build_info"
)
```

Create `internal/license/metrics.go`:

```go
package license

// SeatSample builds a seats_total/seats_used sample with the canonical
// {instance,product,unit,vendor} label set (sorted by key).
func SeatSample(name, vendor, product, unit, instance string, v float64) Sample {
 return Sample{
  Name: name,
  Labels: []Label{
   {Key: "instance", Value: instance},
   {Key: "product", Value: product},
   {Key: "unit", Value: unit},
   {Key: "vendor", Value: vendor},
  },
  Value: v,
 }
}

// ExpirationSample builds a license_expiration_timestamp_seconds sample
// ({instance,product,vendor}). Callers omit it entirely for perpetual licenses.
func ExpirationSample(vendor, product, instance string, tsUnix float64) Sample {
 return Sample{
  Name: MetricExpiration,
  Labels: []Label{
   {Key: "instance", Value: instance},
   {Key: "product", Value: product},
   {Key: "vendor", Value: vendor},
  },
  Value: tsUnix,
 }
}

func vendorInstanceLabels(vendor, instance string) []Label {
 return []Label{
  {Key: "instance", Value: instance},
  {Key: "vendor", Value: vendor},
 }
}

// UpSample builds license_up{vendor,instance}.
func UpSample(vendor, instance string, up bool) Sample {
 v := 0.0
 if up {
  v = 1.0
 }
 return Sample{Name: MetricUp, Labels: vendorInstanceLabels(vendor, instance), Value: v}
}

// LastSuccessSample builds license_collector_last_success_timestamp_seconds.
func LastSuccessSample(vendor, instance string, tsUnix float64) Sample {
 return Sample{Name: MetricLastSuccess, Labels: vendorInstanceLabels(vendor, instance), Value: tsUnix}
}

// ScrapeDurationSample builds license_scrape_duration_seconds.
func ScrapeDurationSample(vendor, instance string, seconds float64) Sample {
 return Sample{Name: MetricScrapeDuration, Labels: vendorInstanceLabels(vendor, instance), Value: seconds}
}

// BuildInfoSample builds the constant license_build_info gauge (value 1).
func BuildInfoSample(version, goVersion string) Sample {
 return Sample{
  Name: MetricBuildInfo,
  Labels: []Label{
   {Key: "goversion", Value: goVersion},
   {Key: "version", Value: version},
  },
  Value: 1,
 }
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/license/ -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add go.mod internal/license/sample.go internal/license/metrics.go internal/license/metrics_test.go
git commit -m "feat(license): core Sample/Label types and metric constructors"
```

---

## Task 2: Snapshot & SnapshotStore (cold start)

**Files:**

- Create: `internal/license/snapshot.go`
- Test: `internal/license/snapshot_test.go`

**Interfaces:**

- Consumes: `Sample`, `BuildInfoSample` (Task 1).
- Produces:
  - `type Snapshot struct { Samples []Sample }`
  - `func ColdStartSnapshot(version, goVersion string) *Snapshot` — a snapshot containing ONLY the build_info sample.
  - `type SnapshotStore struct { ... }`; `func NewSnapshotStore(initial *Snapshot) *SnapshotStore`; `func (s *SnapshotStore) Load() *Snapshot`; `func (s *SnapshotStore) Swap(next *Snapshot)`.

- [ ] **Step 1: Write the failing test**

Create `internal/license/snapshot_test.go`:

```go
package license

import (
 "sync"
 "testing"
)

func TestColdStartSnapshotHasOnlyBuildInfo(t *testing.T) {
 snap := ColdStartSnapshot("1.2.3", "go1.26.4")
 if len(snap.Samples) != 1 {
  t.Fatalf("cold start sample count = %d, want 1", len(snap.Samples))
 }
 if snap.Samples[0].Name != MetricBuildInfo {
  t.Fatalf("cold start metric = %q, want %q", snap.Samples[0].Name, MetricBuildInfo)
 }
}

func TestSnapshotStoreSwapAndLoad(t *testing.T) {
 store := NewSnapshotStore(ColdStartSnapshot("1.2.3", "go1.26.4"))
 next := &Snapshot{Samples: []Sample{UpSample("vmware", "vcsa01", true)}}
 store.Swap(next)
 if got := store.Load(); got != next {
  t.Fatal("Load did not return the swapped snapshot pointer")
 }
}

func TestSnapshotStoreConcurrentAccess(t *testing.T) {
 store := NewSnapshotStore(&Snapshot{})
 var wg sync.WaitGroup
 for i := 0; i < 50; i++ {
  wg.Add(2)
  go func() { defer wg.Done(); store.Swap(&Snapshot{Samples: []Sample{{Name: "x"}}}) }()
  go func() { defer wg.Done(); _ = store.Load() }()
 }
 wg.Wait()
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/license/ -run TestSnapshot -v`
Expected: FAIL — `ColdStartSnapshot` / `NewSnapshotStore` undefined.

- [ ] **Step 3: Write minimal implementation**

Create `internal/license/snapshot.go`:

```go
package license

import "sync"

// Snapshot is an immutable set of samples produced by one collection cycle.
// Callers MUST NOT mutate Samples after publishing to a SnapshotStore.
type Snapshot struct {
 Samples []Sample
}

// ColdStartSnapshot is served before the first collection cycle completes: it
// carries only license_build_info so no target series (license_up, seats_*)
// flap or read as a transient zero (design spec §2).
func ColdStartSnapshot(version, goVersion string) *Snapshot {
 return &Snapshot{Samples: []Sample{BuildInfoSample(version, goVersion)}}
}

// SnapshotStore holds the current snapshot behind an RWMutex pointer-swap.
type SnapshotStore struct {
 mu  sync.RWMutex
 cur *Snapshot
}

func NewSnapshotStore(initial *Snapshot) *SnapshotStore {
 return &SnapshotStore{cur: initial}
}

func (s *SnapshotStore) Load() *Snapshot {
 s.mu.RLock()
 defer s.mu.RUnlock()
 return s.cur
}

func (s *SnapshotStore) Swap(next *Snapshot) {
 s.mu.Lock()
 s.cur = next
 s.mu.Unlock()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/license/ -race -run TestSnapshot -v`
Expected: PASS (including the race detector on concurrent access).

- [ ] **Step 5: Commit**

```bash
git add internal/license/snapshot.go internal/license/snapshot_test.go
git commit -m "feat(license): immutable Snapshot + RWMutex SnapshotStore with cold-start"
```

---

## Task 3: Source interface & collection loop

**Files:**

- Create: `internal/license/source.go`, `internal/license/collector.go`
- Test: `internal/license/collector_test.go`

**Interfaces:**

- Consumes: `Sample`, `Snapshot`, `SnapshotStore`, `UpSample`, `LastSuccessSample`, `ScrapeDurationSample`, `BuildInfoSample` (Tasks 1–2).
- Produces:
  - `type Source interface { Vendor() string; Instance() string; Collect(ctx context.Context) ([]Sample, error) }`
  - `type Collector struct { ... }`
  - `func NewCollector(sources []Source, store *SnapshotStore, version, goVersion string, limit int, now func() time.Time) *Collector`
  - `func (c *Collector) CollectOnce(ctx context.Context) *Snapshot` — fans out; on success appends the source's samples + `up=1` + `last_success` + `scrape_duration`; on error appends only `up=0` + `scrape_duration` (absent-not-zero: no seats). Always appends `build_info`. Swaps the snapshot into the store and returns it.
  - `func (c *Collector) Run(ctx context.Context, interval time.Duration)` — immediate first cycle, then ticks until ctx is done.

- [ ] **Step 1: Write the failing test**

Create `internal/license/collector_test.go`:

```go
package license

import (
 "context"
 "errors"
 "testing"
 "time"
)

type fakeSource struct {
 vendor, instance string
 samples          []Sample
 err              error
}

func (f fakeSource) Vendor() string   { return f.vendor }
func (f fakeSource) Instance() string { return f.instance }
func (f fakeSource) Collect(context.Context) ([]Sample, error) {
 return f.samples, f.err
}

func countByName(samples []Sample, name string) int {
 n := 0
 for _, s := range samples {
  if s.Name == name {
   n++
  }
 }
 return n
}

func upValue(t *testing.T, samples []Sample, vendor, instance string) float64 {
 t.Helper()
 for _, s := range samples {
  if s.Name != MetricUp {
   continue
  }
  v, _ := labelValue(s, "vendor")
  i, _ := labelValue(s, "instance")
  if v == vendor && i == instance {
   return s.Value
  }
 }
 t.Fatalf("no license_up for %s/%s", vendor, instance)
 return -1
}

func fixedClock() time.Time { return time.Unix(1_700_000_000, 0) }

func TestCollectOnceMergesHealthyAndFailedSources(t *testing.T) {
 good := fakeSource{
  vendor: "vmware", instance: "vcsa01",
  samples: []Sample{SeatSample(MetricSeatsTotal, "vmware", "p", "cpuPackage", "vcsa01", 512)},
 }
 bad := fakeSource{vendor: "microsoft", instance: "tenant-a", err: errors.New("boom")}

 store := NewSnapshotStore(ColdStartSnapshot("v", "go"))
 c := NewCollector([]Source{good, bad}, store, "v", "go", 4, fixedClock)
 snap := c.CollectOnce(context.Background())

 if got := upValue(t, snap.Samples, "vmware", "vcsa01"); got != 1 {
  t.Fatalf("good up = %v, want 1", got)
 }
 if got := upValue(t, snap.Samples, "microsoft", "tenant-a"); got != 0 {
  t.Fatalf("bad up = %v, want 0", got)
 }
 // Failed source must NOT emit any seats (absent-not-zero).
 for _, s := range snap.Samples {
  if s.Name == MetricSeatsTotal {
   if v, _ := labelValue(s, "vendor"); v == "microsoft" {
    t.Fatal("failed source emitted seats_total")
   }
  }
 }
 if countByName(snap.Samples, MetricBuildInfo) != 1 {
  t.Fatal("expected exactly one build_info")
 }
 // Healthy source records last_success; failed source does not.
 if countByName(snap.Samples, MetricLastSuccess) != 1 {
  t.Fatalf("last_success count = %d, want 1", countByName(snap.Samples, MetricLastSuccess))
 }
 if store.Load() != snap {
  t.Fatal("CollectOnce did not publish the snapshot")
 }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/license/ -run TestCollectOnce -v`
Expected: FAIL — `NewCollector` / `Source` undefined.

- [ ] **Step 3: Write minimal implementation**

Create `internal/license/source.go`:

```go
package license

import "context"

// Source collects license facts from a single configured target (one tenant or
// one vCenter). It returns samples already carrying vendor+instance labels; the
// collection loop stamps health/duration metrics around it.
type Source interface {
 Vendor() string
 Instance() string
 Collect(ctx context.Context) ([]Sample, error)
}
```

Create `internal/license/collector.go`:

```go
package license

import (
 "context"
 "sync"
 "time"

 "github.com/sirupsen/logrus"
 "golang.org/x/sync/errgroup"
)

// Collector runs the background collection loop and publishes snapshots.
type Collector struct {
 sources   []Source
 store     *SnapshotStore
 version   string
 goVersion string
 limit     int
 now       func() time.Time
}

func NewCollector(sources []Source, store *SnapshotStore, version, goVersion string, limit int, now func() time.Time) *Collector {
 if limit <= 0 {
  limit = len(sources)
 }
 return &Collector{sources: sources, store: store, version: version, goVersion: goVersion, limit: limit, now: now}
}

type sourceResult struct {
 vendor, instance string
 samples          []Sample
 duration         time.Duration
 ok               bool
}

// CollectOnce fans out over every source, builds one snapshot, publishes it, and
// returns it. A per-source failure degrades to license_up=0 (no seats emitted).
func (c *Collector) CollectOnce(ctx context.Context) *Snapshot {
 results := make([]sourceResult, len(c.sources))

 g, gctx := errgroup.WithContext(ctx)
 g.SetLimit(c.limit)
 var mu sync.Mutex
 for i, src := range c.sources {
  i, src := i, src
  g.Go(func() error {
   start := c.now()
   samples, err := src.Collect(gctx)
   dur := c.now().Sub(start)
   mu.Lock()
   results[i] = sourceResult{vendor: src.Vendor(), instance: src.Instance(), samples: samples, duration: dur, ok: err == nil}
   mu.Unlock()
   if err != nil {
    logrus.WithFields(logrus.Fields{"vendor": src.Vendor(), "instance": src.Instance()}).WithError(err).Warn("source collection failed")
   }
   return nil // never abort the cycle on one source's failure
  })
 }
 _ = g.Wait()

 nowUnix := float64(c.now().Unix())
 out := make([]Sample, 0, 16)
 out = append(out, BuildInfoSample(c.version, c.goVersion))
 for _, r := range results {
  out = append(out, ScrapeDurationSample(r.vendor, r.instance, r.duration.Seconds()))
  if r.ok {
   out = append(out, r.samples...)
   out = append(out, UpSample(r.vendor, r.instance, true))
   out = append(out, LastSuccessSample(r.vendor, r.instance, nowUnix))
  } else {
   out = append(out, UpSample(r.vendor, r.instance, false))
  }
 }
 snap := &Snapshot{Samples: out}
 c.store.Swap(snap)
 return snap
}

// Run collects immediately, then every interval until ctx is canceled.
func (c *Collector) Run(ctx context.Context, interval time.Duration) {
 c.CollectOnce(ctx)
 t := time.NewTicker(interval)
 defer t.Stop()
 for {
  select {
  case <-ctx.Done():
   return
  case <-t.C:
   c.CollectOnce(ctx)
  }
 }
}
```

- [ ] **Step 4: Add the dependencies**

Run:

```bash
go get github.com/sirupsen/logrus golang.org/x/sync/errgroup
go mod tidy
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/license/ -race -run TestCollectOnce -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/license/source.go internal/license/collector.go internal/license/collector_test.go go.mod go.sum
git commit -m "feat(license): Source interface + fan-out collection loop with graceful degradation"
```

---

## Task 4: Prometheus unchecked collector

**Files:**

- Create: `internal/license/prometheus.go`
- Test: `internal/license/prometheus_test.go`

**Interfaces:**

- Consumes: `SnapshotStore`, `Sample` (Tasks 1–2).
- Produces:
  - `type PromCollector struct { ... }`; `func NewPromCollector(store *SnapshotStore) *PromCollector`.
  - Implements `prometheus.Collector`: `Describe` sends nothing (unchecked); `Collect` emits one gauge per snapshot sample with that sample's dynamic label set.

- [ ] **Step 1: Write the failing test**

Create `internal/license/prometheus_test.go`:

```go
package license

import (
 "strings"
 "testing"

 "github.com/prometheus/client_golang/prometheus"
 "github.com/prometheus/client_golang/prometheus/testutil"
)

func TestPromCollectorEmitsSnapshotSamples(t *testing.T) {
 store := NewSnapshotStore(&Snapshot{Samples: []Sample{
  SeatSample(MetricSeatsTotal, "microsoft", "M365_E5", "users", "tenant-a", 250),
  UpSample("microsoft", "tenant-a", true),
 }})
 reg := prometheus.NewRegistry()
 reg.MustRegister(NewPromCollector(store))

 expected := `
# HELP license_seats_total Total license capacity purchased.
# TYPE license_seats_total gauge
license_seats_total{instance="tenant-a",product="M365_E5",unit="users",vendor="microsoft"} 250
# HELP license_up 1 if the last refresh of this target succeeded, else 0.
# TYPE license_up gauge
license_up{instance="tenant-a",vendor="microsoft"} 1
`
 if err := testutil.GatherAndCompare(reg, strings.NewReader(expected),
  MetricSeatsTotal, MetricUp); err != nil {
  t.Fatal(err)
 }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/license/ -run TestPromCollector -v`
Expected: FAIL — `NewPromCollector` undefined.

- [ ] **Step 3: Write minimal implementation**

Create `internal/license/prometheus.go`:

```go
package license

import "github.com/prometheus/client_golang/prometheus"

// helpText maps each metric name to its HELP string.
var helpText = map[string]string{
 MetricSeatsTotal:     "Total license capacity purchased.",
 MetricSeatsUsed:      "Currently consumed license capacity.",
 MetricExpiration:     "License expiration as a Unix timestamp (absent when perpetual).",
 MetricUp:             "1 if the last refresh of this target succeeded, else 0.",
 MetricLastSuccess:    "Unix timestamp of the last successful collection for this target.",
 MetricScrapeDuration: "Duration of the last collection for this target, in seconds.",
 MetricBuildInfo:      "Build information; constant 1.",
}

// PromCollector is an unchecked prometheus.Collector: Describe sends nothing, so
// the emitted metric-name/label set may vary snapshot to snapshot.
type PromCollector struct {
 store *SnapshotStore
}

func NewPromCollector(store *SnapshotStore) *PromCollector { return &PromCollector{store: store} }

func (p *PromCollector) Describe(chan<- *prometheus.Desc) {} // unchecked

func (p *PromCollector) Collect(ch chan<- prometheus.Metric) {
 snap := p.store.Load()
 if snap == nil {
  return
 }
 for _, s := range snap.Samples {
  keys := make([]string, len(s.Labels))
  vals := make([]string, len(s.Labels))
  for i, l := range s.Labels {
   keys[i] = l.Key
   vals[i] = l.Value
  }
  help := helpText[s.Name]
  desc := prometheus.NewDesc(s.Name, help, keys, nil)
  ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, s.Value, vals...)
 }
}
```

- [ ] **Step 4: Add the dependency & run**

Run:

```bash
go get github.com/prometheus/client_golang/prometheus
go mod tidy
go test ./internal/license/ -run TestPromCollector -v
```

Expected: PASS.

- [ ] **Step 5: Add the label-key parity invariant test**

Append to `internal/license/prometheus_test.go`:

```go
func TestLabelKeyInvariantPerMetricName(t *testing.T) {
 // Every constructor for a given metric name must yield the same label keys.
 samples := []Sample{
  SeatSample(MetricSeatsTotal, "vmware", "a", "cores", "v1", 1),
  SeatSample(MetricSeatsTotal, "microsoft", "b", "users", "t1", 2),
  UpSample("vmware", "v1", true),
  UpSample("microsoft", "t1", false),
 }
 keysByName := map[string][]string{}
 for _, s := range samples {
  var keys []string
  for _, l := range s.Labels {
   keys = append(keys, l.Key)
  }
  joined := strings.Join(keys, ",")
  if prev, ok := keysByName[s.Name]; ok && strings.Join(prev, ",") != joined {
   t.Fatalf("metric %q has inconsistent label keys: %v vs %v", s.Name, prev, keys)
  }
  keysByName[s.Name] = keys
 }
}
```

Run: `go test ./internal/license/ -run TestLabelKeyInvariant -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/license/prometheus.go internal/license/prometheus_test.go go.mod go.sum
git commit -m "feat(license): unchecked Prometheus collector + label-key parity test"
```

---

## Task 5: OTLP observable gauges

**Files:**

- Create: `internal/license/otlp.go`
- Test: `internal/license/otlp_test.go`

**Interfaces:**

- Consumes: `SnapshotStore`, `Sample`, metric-name consts (Tasks 1–2).
- Produces:
  - `func RegisterOTLP(meter metric.Meter, store *SnapshotStore) error` — registers one Float64ObservableGauge per metric name; each callback reads the latest snapshot and observes its matching samples at observation time (never back-dated — design spec §2, review §2.6).
  - `func Resource(version, instanceID string) *resource.Resource` — `service.name=licenses_exporter`, `service.version`, `service.instance.id`.

- [ ] **Step 1: Write the failing test**

Create `internal/license/otlp_test.go`:

```go
package license

import (
 "context"
 "testing"

 "go.opentelemetry.io/otel/sdk/metric"
 "go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestRegisterOTLPObservesSnapshot(t *testing.T) {
 store := NewSnapshotStore(&Snapshot{Samples: []Sample{
  SeatSample(MetricSeatsUsed, "microsoft", "M365_E5", "users", "tenant-a", 242),
 }})
 reader := metric.NewManualReader()
 provider := metric.NewMeterProvider(metric.WithReader(reader))
 meter := provider.Meter("licenses_exporter")
 if err := RegisterOTLP(meter, store); err != nil {
  t.Fatalf("RegisterOTLP: %v", err)
 }

 var rm metricdata.ResourceMetrics
 if err := reader.Collect(context.Background(), &rm); err != nil {
  t.Fatalf("collect: %v", err)
 }

 var found bool
 for _, sm := range rm.ScopeMetrics {
  for _, m := range sm.Metrics {
   if m.Name != MetricSeatsUsed {
    continue
   }
   g, ok := m.Data.(metricdata.Gauge[float64])
   if !ok {
    t.Fatalf("%s is not a float64 gauge", m.Name)
   }
   for _, dp := range g.DataPoints {
    if dp.Value == 242 {
     found = true
    }
   }
  }
 }
 if !found {
  t.Fatal("license_seats_used=242 not observed via OTLP ManualReader")
 }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/license/ -run TestRegisterOTLP -v`
Expected: FAIL — `RegisterOTLP` undefined.

- [ ] **Step 3: Write minimal implementation**

Create `internal/license/otlp.go`:

```go
package license

import (
 "context"

 "go.opentelemetry.io/otel/attribute"
 "go.opentelemetry.io/otel/metric"
 "go.opentelemetry.io/otel/sdk/resource"
 semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// allMetricNames is the fixed set of observable gauges we register.
var allMetricNames = []string{
 MetricSeatsTotal, MetricSeatsUsed, MetricExpiration,
 MetricUp, MetricLastSuccess, MetricScrapeDuration, MetricBuildInfo,
}

// RegisterOTLP registers one observable gauge per metric name. Each callback
// reads the current snapshot and observes its matching samples at OBSERVATION
// time (points are not back-dated; data age is carried by
// license_collector_last_success_timestamp_seconds).
func RegisterOTLP(meter metric.Meter, store *SnapshotStore) error {
 for _, name := range allMetricNames {
  name := name
  g, err := meter.Float64ObservableGauge(name, metric.WithDescription(helpText[name]))
  if err != nil {
   return err
  }
  _, err = meter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
   snap := store.Load()
   if snap == nil {
    return nil
   }
   for _, s := range snap.Samples {
    if s.Name != name {
     continue
    }
    attrs := make([]attribute.KeyValue, len(s.Labels))
    for i, l := range s.Labels {
     attrs[i] = attribute.String(l.Key, l.Value)
    }
    o.ObserveFloat64(g, s.Value, metric.WithAttributes(attrs...))
   }
   return nil
  }, g)
  if err != nil {
   return err
  }
 }
 return nil
}

// Resource builds the OTLP resource attributes for the exporter.
func Resource(version, instanceID string) *resource.Resource {
 return resource.NewWithAttributes(
  semconv.SchemaURL,
  semconv.ServiceName("licenses_exporter"),
  semconv.ServiceVersion(version),
  semconv.ServiceInstanceID(instanceID),
 )
}
```

- [ ] **Step 4: Add the dependencies & run**

Run:

```bash
go get go.opentelemetry.io/otel go.opentelemetry.io/otel/metric go.opentelemetry.io/otel/sdk/metric go.opentelemetry.io/otel/sdk/resource
go mod tidy
go test ./internal/license/ -run TestRegisterOTLP -v
```

Expected: PASS. (If the `semconv/v1.26.0` import path is unavailable for the resolved otel version, adjust to the version directory that exists under `go.opentelemetry.io/otel/semconv/` — confirm with `go doc go.opentelemetry.io/otel/semconv` after `go mod tidy`.)

- [ ] **Step 5: Commit**

```bash
git add internal/license/otlp.go internal/license/otlp_test.go go.mod go.sum
git commit -m "feat(license): OTLP observable gauges (observation-time) + resource attrs"
```

---

## Task 6: Configuration (YAML, env expansion, validation)

**Files:**

- Create: `internal/config/config.go`, `internal/config/dotenv.go`, `config.yaml`
- Test: `internal/config/config_test.go`

**Interfaces:**

- Produces:
  - `type Config struct { Collection CollectionConfig; Collectors CollectorsConfig }`
  - `type CollectionConfig struct { Interval time.Duration }` (`interval` parsed from a Go duration string like `2h`).
  - `type CollectorsConfig struct { M365 M365Raw; VMware VMwareRaw }` where each `*Raw` is `map[string]any`-free typed config (fields defined here to avoid an import cycle; Tasks 7–8 consume them).
  - `func Load(path string) (*Config, error)` — godotenv load → read file → `${ENV}` expand → YAML unmarshal → validate.
  - `func Expand(s string) (string, error)` — replaces `${VAR}`; errors on unset.

**Note on layering:** to avoid an import cycle, the vendor config *structs* live in `internal/config` (`M365Raw`, `VMwareRaw`) and Tasks 7–8 map them into vendor-package types. Alternatively define them in the vendor packages and import those here — this plan keeps them in `internal/config` since config owns parsing.

- [ ] **Step 1: Write the failing test**

Create `internal/config/config_test.go`:

```go
package config

import (
 "os"
 "path/filepath"
 "testing"
 "time"
)

func writeTemp(t *testing.T, body string) string {
 t.Helper()
 p := filepath.Join(t.TempDir(), "config.yaml")
 if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
  t.Fatal(err)
 }
 return p
}

func TestLoadExpandsEnvAndParsesInterval(t *testing.T) {
 t.Setenv("VC_PASS", "s3cret")
 p := writeTemp(t, `
collection:
  interval: 2h
collectors:
  vmware:
    enabled: true
    vcenters:
      - instance: vcsa01
        host: https://vc/sdk
        username: svc
        password: ${VC_PASS}
`)
 cfg, err := Load(p)
 if err != nil {
  t.Fatalf("Load: %v", err)
 }
 if cfg.Collection.Interval != 2*time.Hour {
  t.Fatalf("interval = %v, want 2h", cfg.Collection.Interval)
 }
 if got := cfg.Collectors.VMware.VCenters[0].Password; got != "s3cret" {
  t.Fatalf("password = %q, want expanded", got)
 }
}

func TestLoadFailsOnUnsetEnv(t *testing.T) {
 p := writeTemp(t, `
collection: { interval: 1h }
collectors:
  vmware:
    enabled: true
    vcenters:
      - instance: v1
        host: h
        username: u
        password: ${DEFINITELY_UNSET_VAR}
`)
 if _, err := Load(p); err == nil {
  t.Fatal("expected error on unset env var")
 }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -v`
Expected: FAIL — `Load` undefined.

- [ ] **Step 3: Write minimal implementation**

Create `internal/config/config.go`:

```go
// Package config loads and validates the exporter configuration.
package config

import (
 "fmt"
 "os"
 "regexp"
 "time"

 "gopkg.in/yaml.v2"
)

type Config struct {
 Collection CollectionConfig `yaml:"collection"`
 Collectors CollectorsConfig `yaml:"collectors"`
}

type CollectionConfig struct {
 Interval time.Duration `yaml:"interval"`
}

type CollectorsConfig struct {
 M365   M365Raw   `yaml:"m365"`
 VMware VMwareRaw `yaml:"vmware"`
}

type M365Raw struct {
 Enabled bool         `yaml:"enabled"`
 Tenants []TenantRaw  `yaml:"tenants"`
}

type TenantRaw struct {
 Instance         string `yaml:"instance"`
 TenantID         string `yaml:"tenantId"`
 ClientID         string `yaml:"clientId"`
 ClientSecret     string `yaml:"clientSecret"`
 ClientSecretFile string `yaml:"clientSecretFile"`
}

type VMwareRaw struct {
 Enabled  bool          `yaml:"enabled"`
 VCenters []VCenterRaw  `yaml:"vcenters"`
}

type VCenterRaw struct {
 Instance           string `yaml:"instance"`
 Host               string `yaml:"host"`
 Username           string `yaml:"username"`
 Password           string `yaml:"password"`
 PasswordFile       string `yaml:"passwordFile"`
 InsecureSkipVerify bool   `yaml:"insecureSkipVerify"`
}

var envRef = regexp.MustCompile(`\$\{([A-Z0-9_]+)\}`)

// Expand replaces ${VAR} references, failing on any unset variable.
func Expand(s string) (string, error) {
 var missing string
 out := envRef.ReplaceAllStringFunc(s, func(m string) string {
  name := envRef.FindStringSubmatch(m)[1]
  v, ok := os.LookupEnv(name)
  if !ok {
   missing = name
   return m
  }
  return v
 })
 if missing != "" {
  return "", fmt.Errorf("config references unset environment variable %q", missing)
 }
 return out, nil
}

// Load reads .env, expands ${ENV}, unmarshals YAML, and validates.
func Load(path string) (*Config, error) {
 LoadDotEnv(path)
 raw, err := os.ReadFile(path)
 if err != nil {
  return nil, fmt.Errorf("read config: %w", err)
 }
 expanded, err := Expand(string(raw))
 if err != nil {
  return nil, err
 }
 var cfg Config
 if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
  return nil, fmt.Errorf("parse config: %w", err)
 }
 if err := cfg.validate(); err != nil {
  return nil, err
 }
 return &cfg, nil
}

func (c *Config) validate() error {
 if c.Collection.Interval <= 0 {
  return fmt.Errorf("collection.interval must be > 0")
 }
 if !c.Collectors.M365.Enabled && !c.Collectors.VMware.Enabled {
  return fmt.Errorf("no collectors enabled")
 }
 for _, v := range c.Collectors.VMware.VCenters {
  if v.Instance == "" || v.Host == "" {
   return fmt.Errorf("vmware vcenter entry missing instance or host")
  }
 }
 for _, t := range c.Collectors.M365.Tenants {
  if t.Instance == "" || t.TenantID == "" {
   return fmt.Errorf("m365 tenant entry missing instance or tenantId")
  }
 }
 return nil
}
```

Create `internal/config/dotenv.go`:

```go
package config

import (
 "path/filepath"

 "github.com/joho/godotenv"
)

// LoadDotEnv loads a .env from the CWD, then from the config file's directory,
// BEFORE ${ENV} interpolation. godotenv never overrides an already-set variable,
// so real secret injection always wins. Missing .env files are ignored.
func LoadDotEnv(cfgPath string) {
 _ = godotenv.Load()
 if dir := filepath.Dir(cfgPath); dir != "." && dir != "" {
  _ = godotenv.Load(filepath.Join(dir, ".env"))
 }
}
```

- [ ] **Step 4: `interval` as a Go duration**

`yaml.v2` does not natively unmarshal `2h` into `time.Duration`. Add a custom unmarshaler. Append to `config.go`:

```go
// UnmarshalYAML lets collection.interval accept a Go duration string ("2h").
func (c *CollectionConfig) UnmarshalYAML(unmarshal func(interface{}) error) error {
 var raw struct {
  Interval string `yaml:"interval"`
 }
 if err := unmarshal(&raw); err != nil {
  return err
 }
 d, err := time.ParseDuration(raw.Interval)
 if err != nil {
  return fmt.Errorf("collection.interval %q: %w", raw.Interval, err)
 }
 c.Interval = d
 return nil
}
```

- [ ] **Step 5: Add deps, write the sample config, run**

Run:

```bash
go get gopkg.in/yaml.v2 github.com/joho/godotenv
go mod tidy
go test ./internal/config/ -v
```

Expected: PASS.

Create `config.yaml`:

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
        clientSecret: ${M365_CLIENT_SECRET}
  vmware:
    enabled: true
    vcenters:
      - instance: vcsa01
        host: ${VCENTER_HOST}
        username: ${VCENTER_USER}
        password: ${VCENTER_PASSWORD}
        insecureSkipVerify: false
```

- [ ] **Step 6: Commit**

```bash
git add internal/config/ config.yaml go.mod go.sum
git commit -m "feat(config): YAML load with env expansion, duration interval, validation, dotenv"
```

---

## Task 7: VMware collector (govmomi, stateless, unlimited-aware)

**Files:**

- Create: `internal/vmware/parse.go`, `internal/vmware/source.go`, `internal/vmware/vmware.go`
- Test: `internal/vmware/parse_test.go`, `internal/vmware/source_test.go`

**Interfaces:**

- Consumes: `license.Sample`, `license.Source`, `license.SeatSample`, `license.ExpirationSample` (Tasks 1–3); `config.VMwareRaw`, `config.VCenterRaw` (Task 6).
- Produces:
  - `func licensesToSamples(instance string, infos []types.LicenseManagerLicenseInfo) []license.Sample` — pure. `Total > 0` → `seats_total`; always `seats_used`; `CostUnit` → `unit` (fallback `"unit"` if empty); `Name` → `product`; `expirationDate` property (a `time.Time`) → `ExpirationSample`; **`Total <= 0` (unlimited) omits `seats_total`**.
  - `type source struct { instance, host, username, password string; insecure bool }` implementing `license.Source`.
  - `func NewSources(cfg config.VMwareRaw) ([]license.Source, error)`.

> **Confirm-at-impl (spec §11):** the exact `LicenseManagerLicenseInfo.Used` field type (int32 vs *int32) and the `expirationDate` property value type. Run `go doc github.com/vmware/govmomi/vim25/types.LicenseManagerLicenseInfo` after adding the dep and adjust the two marked spots. The pure-parse fixtures make the compiler enforce whatever the real types are.

- [ ] **Step 1: Add the dependency**

Run:

```bash
go get github.com/vmware/govmomi
go doc github.com/vmware/govmomi/vim25/types.LicenseManagerLicenseInfo
```

Note the `Used` and `Properties` field types from the `go doc` output before writing parse.

- [ ] **Step 2: Write the failing parse test**

Create `internal/vmware/parse_test.go`:

```go
package vmware

import (
 "testing"
 "time"

 "github.com/vmware/govmomi/vim25/types"
)

type sampleView struct {
 name  string
 value float64
 unit  string
 prod  string
}

func find(samples []sampleView, name string) (sampleView, bool) {
 for _, s := range samples {
  if s.name == name {
   return s, true
  }
 }
 return sampleView{}, false
}

func view(instance string, infos []types.LicenseManagerLicenseInfo) []sampleView {
 out := []sampleView{}
 for _, s := range licensesToSamples(instance, infos) {
  v := sampleView{name: s.Name, value: s.Value}
  for _, l := range s.Labels {
   switch l.Key {
   case "unit":
    v.unit = l.Value
   case "product":
    v.prod = l.Value
   }
  }
  out = append(out, v)
 }
 return out
}

func TestLimitedLicenseEmitsTotalUsedExpiration(t *testing.T) {
 exp := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
 infos := []types.LicenseManagerLicenseInfo{{
  Name:     "vSphere 8 Enterprise Plus",
  Total:    512,
  Used:     420,
  CostUnit: "cpuPackage",
  Properties: []types.KeyAnyValue{
   {Key: "expirationDate", Value: exp},
  },
 }}
 sv := view("vcsa01", infos)
 if s, ok := find(sv, "license_seats_total"); !ok || s.value != 512 || s.unit != "cpuPackage" {
  t.Fatalf("seats_total wrong: %+v ok=%v", s, ok)
 }
 if s, ok := find(sv, "license_seats_used"); !ok || s.value != 420 {
  t.Fatalf("seats_used wrong: %+v ok=%v", s, ok)
 }
 if s, ok := find(sv, "license_expiration_timestamp_seconds"); !ok || s.value != float64(exp.Unix()) {
  t.Fatalf("expiration wrong: %+v ok=%v", s, ok)
 }
}

func TestUnlimitedLicenseOmitsTotal(t *testing.T) {
 infos := []types.LicenseManagerLicenseInfo{{
  Name:     "Evaluation Mode",
  Total:    0, // unlimited
  Used:     3,
  CostUnit: "cpuPackage",
 }}
 sv := view("vcsa01", infos)
 if _, ok := find(sv, "license_seats_total"); ok {
  t.Fatal("unlimited license must omit seats_total")
 }
 if s, ok := find(sv, "license_seats_used"); !ok || s.value != 3 {
  t.Fatalf("seats_used wrong: %+v ok=%v", s, ok)
 }
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/vmware/ -run TestLimited -v`
Expected: FAIL — `licensesToSamples` undefined.

- [ ] **Step 4: Write the pure parse implementation**

Create `internal/vmware/parse.go`:

```go
package vmware

import (
 "time"

 "github.com/fjacquet/licenses_exporter/internal/license"
 "github.com/vmware/govmomi/vim25/types"
)

const vendor = "vmware"

// licensesToSamples maps vSphere LicenseManager entries to license samples.
// Unlimited licenses (Total <= 0) omit seats_total (absent-not-zero).
func licensesToSamples(instance string, infos []types.LicenseManagerLicenseInfo) []license.Sample {
 var out []license.Sample
 for _, info := range infos {
  unit := info.CostUnit
  if unit == "" {
   unit = "unit"
  }
  product := info.Name
  if info.Total > 0 {
   out = append(out, license.SeatSample(license.MetricSeatsTotal, vendor, product, unit, instance, float64(info.Total)))
  }
  out = append(out, license.SeatSample(license.MetricSeatsUsed, vendor, product, unit, instance, float64(info.Used)))
  if exp, ok := expiration(info.Properties); ok {
   out = append(out, license.ExpirationSample(vendor, product, instance, float64(exp.Unix())))
  }
 }
 return out
}

// expiration extracts the expirationDate property; absent for perpetual licenses.
func expiration(props []types.KeyAnyValue) (time.Time, bool) {
 for _, p := range props {
  if p.Key != "expirationDate" {
   continue
  }
  if t, ok := p.Value.(time.Time); ok {
   return t, true
  }
 }
 return time.Time{}, false
}
```

- [ ] **Step 5: Run parse test to verify it passes**

Run: `go test ./internal/vmware/ -run "TestLimited|TestUnlimited" -v`
Expected: PASS.

- [ ] **Step 6: Write the Source and NewSources**

Create `internal/vmware/source.go`:

```go
package vmware

import (
 "context"
 "fmt"
 "net/url"

 "github.com/fjacquet/licenses_exporter/internal/license"
 "github.com/vmware/govmomi"
 vlicense "github.com/vmware/govmomi/license"
 "github.com/vmware/govmomi/vim25/soap"
)

type source struct {
 instance string
 host     string
 username string
 password string
 insecure bool
}

func (s *source) Vendor() string   { return vendor }
func (s *source) Instance() string { return s.instance }

// Collect logs in fresh, lists licenses, and logs out — stateless per cycle
// (design spec §6). Logout uses a background context so it runs even if ctx
// was canceled mid-cycle.
func (s *source) Collect(ctx context.Context) ([]license.Sample, error) {
 u, err := soap.ParseURL(s.host)
 if err != nil {
  return nil, fmt.Errorf("parse vcenter url: %w", err)
 }
 u.User = url.UserPassword(s.username, s.password)

 c, err := govmomi.NewClient(ctx, u, s.insecure)
 if err != nil {
  return nil, fmt.Errorf("vcenter login: %w", err)
 }
 defer func() { _ = c.Logout(context.Background()) }()

 infos, err := vlicense.NewManager(c.Client).List(ctx)
 if err != nil {
  return nil, fmt.Errorf("list licenses: %w", err)
 }
 return licensesToSamples(s.instance, infos), nil
}
```

Create `internal/vmware/vmware.go`:

```go
package vmware

import (
 "fmt"
 "os"
 "strings"

 "github.com/fjacquet/licenses_exporter/internal/config"
 "github.com/fjacquet/licenses_exporter/internal/license"
)

// NewSources builds one stateless Source per configured vCenter.
func NewSources(cfg config.VMwareRaw) ([]license.Source, error) {
 if !cfg.Enabled {
  return nil, nil
 }
 var out []license.Source
 for _, v := range cfg.VCenters {
  pw, err := resolveSecret(v.Password, v.PasswordFile)
  if err != nil {
   return nil, fmt.Errorf("vcenter %q: %w", v.Instance, err)
  }
  out = append(out, &source{
   instance: v.Instance,
   host:     v.Host,
   username: v.Username,
   password: pw,
   insecure: v.InsecureSkipVerify,
  })
 }
 return out, nil
}

func resolveSecret(inline, file string) (string, error) {
 if file != "" {
  b, err := os.ReadFile(file)
  if err != nil {
   return "", fmt.Errorf("read secret file: %w", err)
  }
  return strings.TrimSpace(string(b)), nil
 }
 return inline, nil
}
```

- [ ] **Step 7: Write the vcsim integration (smoke) test**

Create `internal/vmware/source_test.go`:

```go
package vmware

import (
 "context"
 "testing"

 "github.com/vmware/govmomi/simulator"
)

// vcsim's default license manager returns the eval license (Total=0). This is a
// wiring smoke test: login → List → logout must succeed, and the unlimited eval
// license must never yield a non-positive seats_total.
func TestCollectAgainstVcsim(t *testing.T) {
 model := simulator.VPX()
 if err := model.Create(); err != nil {
  t.Fatal(err)
 }
 defer model.Remove()
 server := model.Service.NewServer()
 defer server.Close()

 src := &source{instance: "vcsim", host: server.URL.String(), username: "user", password: "pass", insecure: true}
 samples, err := src.Collect(context.Background())
 if err != nil {
  t.Fatalf("Collect against vcsim: %v", err)
 }
 for _, s := range samples {
  if s.Name == "license_seats_total" && s.Value <= 0 {
   t.Fatalf("emitted non-positive seats_total %v (unlimited must be omitted)", s.Value)
  }
 }
}
```

- [ ] **Step 8: Run all vmware tests**

Run:

```bash
go mod tidy
go test ./internal/vmware/ -race -v
```

Expected: PASS (parse + vcsim smoke). If `Collect` fails to compile because `Used` is `*int32`, change `float64(info.Used)` to a nil-guarded helper and re-run.

- [ ] **Step 9: Commit**

```bash
git add internal/vmware/ go.mod go.sum
git commit -m "feat(vmware): govmomi stateless license collector, unlimited-aware, vcsim-tested"
```

---

## Task 8: Microsoft 365 collector (msgraph-sdk-go, paginated)

**Files:**

- Create: `internal/m365/parse.go`, `internal/m365/source.go`, `internal/m365/graph.go`, `internal/m365/m365.go`
- Test: `internal/m365/parse_test.go`, `internal/m365/source_test.go`

**Interfaces:**

- Consumes: `license.Sample`, `license.Source`, `license.SeatSample` (Tasks 1–3); `config.M365Raw`, `config.TenantRaw` (Task 6).
- Produces:
  - `func skusToSamples(instance string, skus []models.SubscribedSkuable) []license.Sample` — pure; `unit="users"`; nil-guards every getter (absent-not-zero); no expiration series.
  - `type skuLister interface { listSkus(ctx context.Context) ([]models.SubscribedSkuable, error) }` — the SDK seam.
  - `type source struct { instance string; lister skuLister }` implementing `license.Source`.
  - `type graphSkuLister struct { client *msgraphsdk.GraphServiceClient }` — real impl using `SubscribedSkus().Get` + `PageIterator`.
  - `func NewSources(cfg config.M365Raw) ([]license.Source, error)`.

- [ ] **Step 1: Add the dependencies**

Run:

```bash
go get github.com/microsoftgraph/msgraph-sdk-go github.com/microsoftgraph/msgraph-sdk-go-core github.com/Azure/azure-sdk-for-go/sdk/azidentity
go mod tidy
```

- [ ] **Step 2: Write the failing parse test**

Create `internal/m365/parse_test.go`:

```go
package m365

import (
 "testing"

 "github.com/microsoftgraph/msgraph-sdk-go/models"
)

func ptr[T any](v T) *T { return &v }

func TestSkusToSamples(t *testing.T) {
 sku := models.NewSubscribedSku()
 sku.SetSkuPartNumber(ptr("SPE_E5"))
 sku.SetConsumedUnits(ptr(int32(242)))
 detail := models.NewLicenseUnitsDetail()
 detail.SetEnabled(ptr(int32(250)))
 sku.SetPrepaidUnits(detail)

 samples := skusToSamples("tenant-a", []models.SubscribedSkuable{sku})

 var gotTotal, gotUsed float64
 var product, unit string
 for _, s := range samples {
  for _, l := range s.Labels {
   if l.Key == "product" {
    product = l.Value
   }
   if l.Key == "unit" {
    unit = l.Value
   }
  }
  switch s.Name {
  case "license_seats_total":
   gotTotal = s.Value
  case "license_seats_used":
   gotUsed = s.Value
  }
 }
 if gotTotal != 250 || gotUsed != 242 {
  t.Fatalf("total=%v used=%v, want 250/242", gotTotal, gotUsed)
 }
 if product != "SPE_E5" || unit != "users" {
  t.Fatalf("product=%q unit=%q", product, unit)
 }
}

func TestSkusToSamplesNilGuards(t *testing.T) {
 sku := models.NewSubscribedSku() // all fields nil
 samples := skusToSamples("tenant-a", []models.SubscribedSkuable{sku})
 // No panics; with no counts, no seats emitted (absent-not-zero).
 for _, s := range samples {
  if s.Name == "license_seats_total" || s.Name == "license_seats_used" {
   t.Fatalf("emitted %s from a nil-count SKU", s.Name)
  }
 }
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/m365/ -run TestSkusToSamples -v`
Expected: FAIL — `skusToSamples` undefined.

- [ ] **Step 4: Write the pure parse implementation**

Create `internal/m365/parse.go`:

```go
package m365

import (
 "github.com/fjacquet/licenses_exporter/internal/license"
 "github.com/microsoftgraph/msgraph-sdk-go/models"
)

const (
 vendor = "microsoft"
 unit   = "users"
)

// skusToSamples maps subscribedSkus to license samples. Every getter is
// nil-guarded: a missing count yields an absent sample, never a fake 0.
func skusToSamples(instance string, skus []models.SubscribedSkuable) []license.Sample {
 var out []license.Sample
 for _, sku := range skus {
  if sku == nil {
   continue
  }
  product := ""
  if p := sku.GetSkuPartNumber(); p != nil {
   product = *p
  }
  if pre := sku.GetPrepaidUnits(); pre != nil {
   if enabled := pre.GetEnabled(); enabled != nil {
    out = append(out, license.SeatSample(license.MetricSeatsTotal, vendor, product, unit, instance, float64(*enabled)))
   }
  }
  if consumed := sku.GetConsumedUnits(); consumed != nil {
   out = append(out, license.SeatSample(license.MetricSeatsUsed, vendor, product, unit, instance, float64(*consumed)))
  }
 }
 return out
}
```

- [ ] **Step 5: Run parse test to verify it passes**

Run: `go test ./internal/m365/ -run TestSkusToSamples -v`
Expected: PASS.

- [ ] **Step 6: Write the Source over the seam + a fake-lister test**

Create `internal/m365/source.go`:

```go
package m365

import (
 "context"

 "github.com/fjacquet/licenses_exporter/internal/license"
 "github.com/microsoftgraph/msgraph-sdk-go/models"
)

// skuLister isolates the Graph SDK so the Source is unit-testable.
type skuLister interface {
 listSkus(ctx context.Context) ([]models.SubscribedSkuable, error)
}

type source struct {
 instance string
 lister   skuLister
}

func (s *source) Vendor() string   { return vendor }
func (s *source) Instance() string { return s.instance }

func (s *source) Collect(ctx context.Context) ([]license.Sample, error) {
 skus, err := s.lister.listSkus(ctx)
 if err != nil {
  return nil, err
 }
 return skusToSamples(s.instance, skus), nil
}
```

Create `internal/m365/source_test.go`:

```go
package m365

import (
 "context"
 "testing"

 "github.com/microsoftgraph/msgraph-sdk-go/models"
)

type fakeLister struct {
 skus []models.SubscribedSkuable
 err  error
}

func (f fakeLister) listSkus(context.Context) ([]models.SubscribedSkuable, error) {
 return f.skus, f.err
}

func TestSourceCollectUsesLister(t *testing.T) {
 sku := models.NewSubscribedSku()
 sku.SetSkuPartNumber(ptr("SPB"))
 sku.SetConsumedUnits(ptr(int32(5)))
 src := &source{instance: "tenant-a", lister: fakeLister{skus: []models.SubscribedSkuable{sku}}}

 samples, err := src.Collect(context.Background())
 if err != nil {
  t.Fatal(err)
 }
 found := false
 for _, s := range samples {
  if s.Name == "license_seats_used" && s.Value == 5 {
   found = true
  }
 }
 if !found {
  t.Fatal("expected seats_used=5 from lister SKUs")
 }
}
```

Run: `go test ./internal/m365/ -run TestSourceCollect -v`
Expected: PASS.

- [ ] **Step 7: Write the real Graph lister + NewSources**

Create `internal/m365/graph.go`:

```go
package m365

import (
 "context"

 msgraphsdk "github.com/microsoftgraph/msgraph-sdk-go"
 msgraphcore "github.com/microsoftgraph/msgraph-sdk-go-core"
 "github.com/microsoftgraph/msgraph-sdk-go/models"
)

// graphSkuLister lists subscribedSkus via the Graph SDK, following @odata.nextLink.
type graphSkuLister struct {
 client *msgraphsdk.GraphServiceClient
}

func (g graphSkuLister) listSkus(ctx context.Context) ([]models.SubscribedSkuable, error) {
 page, err := g.client.SubscribedSkus().Get(ctx, nil)
 if err != nil {
  return nil, err
 }
 iterator, err := msgraphcore.NewPageIterator[models.SubscribedSkuable](
  page, g.client.GetAdapter(),
  models.CreateSubscribedSkuCollectionResponseFromDiscriminatorValue,
 )
 if err != nil {
  return nil, err
 }
 var out []models.SubscribedSkuable
 err = iterator.Iterate(ctx, func(item models.SubscribedSkuable) bool {
  if item != nil {
   out = append(out, item)
  }
  return true // keep paging
 })
 return out, err
}
```

Create `internal/m365/m365.go`:

```go
package m365

import (
 "fmt"
 "os"
 "strings"

 "github.com/Azure/azure-sdk-for-go/sdk/azidentity"
 "github.com/fjacquet/licenses_exporter/internal/config"
 "github.com/fjacquet/licenses_exporter/internal/license"
 msgraphsdk "github.com/microsoftgraph/msgraph-sdk-go"
)

// graphScopes requests the app-only default scope; the app registration must be
// granted Organization.Read.All (or Directory.Read.All) — see docs/deployment.
var graphScopes = []string{"https://graph.microsoft.com/.default"}

// NewSources builds one Source per configured tenant.
func NewSources(cfg config.M365Raw) ([]license.Source, error) {
 if !cfg.Enabled {
  return nil, nil
 }
 var out []license.Source
 for _, t := range cfg.Tenants {
  secret, err := resolveSecret(t.ClientSecret, t.ClientSecretFile)
  if err != nil {
   return nil, fmt.Errorf("m365 tenant %q: %w", t.Instance, err)
  }
  cred, err := azidentity.NewClientSecretCredential(t.TenantID, t.ClientID, secret, nil)
  if err != nil {
   return nil, fmt.Errorf("m365 tenant %q credential: %w", t.Instance, err)
  }
  client, err := msgraphsdk.NewGraphServiceClientWithCredentials(cred, graphScopes)
  if err != nil {
   return nil, fmt.Errorf("m365 tenant %q client: %w", t.Instance, err)
  }
  out = append(out, &source{instance: t.Instance, lister: graphSkuLister{client: client}})
 }
 return out, nil
}

func resolveSecret(inline, file string) (string, error) {
 if file != "" {
  b, err := os.ReadFile(file)
  if err != nil {
   return "", fmt.Errorf("read secret file: %w", err)
  }
  return strings.TrimSpace(string(b)), nil
 }
 return inline, nil
}
```

> **Confirm-at-impl:** the `msgraph-sdk-go-core` import path's package name and the `NewPageIterator` generic signature can differ across SDK majors. After `go mod tidy`, run `go build ./internal/m365/` and, if it fails, check `go doc github.com/microsoftgraph/msgraph-sdk-go-core.NewPageIterator` and adjust the type argument / constructor accordingly.

- [ ] **Step 8: Run all m365 tests + build**

Run:

```bash
go mod tidy
go build ./internal/m365/
go test ./internal/m365/ -race -v
```

Expected: build OK; parse + source tests PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/m365/ go.mod go.sum
git commit -m "feat(m365): msgraph-sdk-go subscribedSkus collector (paginated) behind a testable seam"
```

---

## Task 9: main.go wiring (server-before-collect, /health, reload, CLI flags)

**Files:**

- Create: `internal/app/app.go`, `internal/app/health.go`, `main.go`
- Test: `internal/app/app_test.go`, `internal/app/health_test.go`

**Interfaces:**

- Consumes: everything above.
- Produces:
  - `func BuildSources(cfg *config.Config) ([]license.Source, error)` — concatenates vmware + m365 sources.
  - `type Health struct { ... }`; `func (h *Health) SetReady()`; `func (h *Health) ServeHTTP(w, r)` — 503 `starting` until ready, then 200 `ok`.
  - `func Run(ctx context.Context, cfg *config.Config, version, addr string, once bool) error` — the wiring (used by main; `once` runs a single cycle and returns).

- [ ] **Step 1: Write the failing tests**

Create `internal/app/health_test.go`:

```go
package app

import (
 "net/http"
 "net/http/httptest"
 "testing"
)

func TestHealthStartingThenOk(t *testing.T) {
 h := &Health{}
 rec := httptest.NewRecorder()
 h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
 if rec.Code != http.StatusServiceUnavailable {
  t.Fatalf("pre-ready code = %d, want 503", rec.Code)
 }
 h.SetReady()
 rec = httptest.NewRecorder()
 h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
 if rec.Code != http.StatusOK {
  t.Fatalf("post-ready code = %d, want 200", rec.Code)
 }
}
```

Create `internal/app/app_test.go`:

```go
package app

import (
 "testing"
 "time"

 "github.com/fjacquet/licenses_exporter/internal/config"
)

func TestBuildSourcesRespectsEnabledFlags(t *testing.T) {
 cfg := &config.Config{
  Collection: config.CollectionConfig{Interval: time.Hour},
  Collectors: config.CollectorsConfig{
   VMware: config.VMwareRaw{Enabled: true, VCenters: []config.VCenterRaw{{Instance: "v1", Host: "https://vc/sdk", Username: "u", Password: "p"}}},
   M365:   config.M365Raw{Enabled: false},
  },
 }
 sources, err := BuildSources(cfg)
 if err != nil {
  t.Fatal(err)
 }
 if len(sources) != 1 || sources[0].Vendor() != "vmware" {
  t.Fatalf("expected 1 vmware source, got %d", len(sources))
 }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/app/ -v`
Expected: FAIL — `Health` / `BuildSources` undefined.

- [ ] **Step 3: Write health + BuildSources**

Create `internal/app/health.go`:

```go
package app

import (
 "net/http"
 "sync/atomic"
)

// Health reports 503 "starting" until the first collection cycle completes,
// then 200 "ok" (design spec §2).
type Health struct {
 ready atomic.Bool
}

func (h *Health) SetReady() { h.ready.Store(true) }

func (h *Health) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
 if h.ready.Load() {
  w.WriteHeader(http.StatusOK)
  _, _ = w.Write([]byte("ok"))
  return
 }
 w.WriteHeader(http.StatusServiceUnavailable)
 _, _ = w.Write([]byte("starting"))
}
```

Create `internal/app/app.go`:

```go
// Package app wires config, collectors, the snapshot store, and the two export
// paths into a running exporter.
package app

import (
 "context"
 "fmt"
 "net/http"
 "runtime"
 "sort"
 "time"

 "github.com/fjacquet/licenses_exporter/internal/config"
 "github.com/fjacquet/licenses_exporter/internal/license"
 "github.com/fjacquet/licenses_exporter/internal/m365"
 "github.com/fjacquet/licenses_exporter/internal/vmware"
 "github.com/prometheus/client_golang/prometheus"
 "github.com/prometheus/client_golang/prometheus/promhttp"
 "github.com/sirupsen/logrus"
)

// BuildSources concatenates every enabled collector's sources.
func BuildSources(cfg *config.Config) ([]license.Source, error) {
 var sources []license.Source
 vm, err := vmware.NewSources(cfg.Collectors.VMware)
 if err != nil {
  return nil, err
 }
 sources = append(sources, vm...)
 ms, err := m365.NewSources(cfg.Collectors.M365)
 if err != nil {
  return nil, err
 }
 sources = append(sources, ms...)
 return sources, nil
}

// Run wires and starts the exporter. If once is true, it runs a single collection
// cycle and returns (used by --once); otherwise it serves until ctx is canceled.
func Run(ctx context.Context, cfg *config.Config, version, addr string, once bool) error {
 sources, err := BuildSources(cfg)
 if err != nil {
  return err
 }
 store := license.NewSnapshotStore(license.ColdStartSnapshot(version, runtime.Version()))
 collector := license.NewCollector(sources, store, version, runtime.Version(), 0, time.Now)

 if once {
  snap := collector.CollectOnce(ctx)
  dumpSamples(snap)
  return nil
 }

 reg := prometheus.NewRegistry()
 reg.MustRegister(license.NewPromCollector(store))

 health := &Health{}
 mux := http.NewServeMux()
 mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
 mux.Handle("/health", health)

 srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
 go func() {
  logrus.WithField("addr", addr).Info("serving /metrics and /health")
  if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
   logrus.WithError(err).Fatal("http server failed")
  }
 }()

 // First cycle, then mark ready; then loop until ctx done.
 collector.CollectOnce(ctx)
 health.SetReady()
 go collector.Run(ctx, cfg.Collection.Interval)

 <-ctx.Done()
 shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
 defer cancel()
 return srv.Shutdown(shutCtx)
}

// dumpSamples prints every sample sorted (exposition style) for --once --debug.
func dumpSamples(snap *license.Snapshot) {
 lines := make([]string, 0, len(snap.Samples))
 for _, s := range snap.Samples {
  lines = append(lines, fmt.Sprintf("%s %g", s.Name, s.Value))
 }
 sort.Strings(lines)
 for _, l := range lines {
  fmt.Println(l)
 }
}
```

- [ ] **Step 4: Run tests**

Run:

```bash
go get github.com/prometheus/client_golang/prometheus/promhttp
go mod tidy
go test ./internal/app/ -v
```

Expected: PASS.

- [ ] **Step 5: Write main.go (cobra CLI + SIGHUP/file-watch reload)**

Create `main.go`:

```go
package main

import (
 "context"
 "os"
 "os/signal"
 "syscall"

 "github.com/fjacquet/licenses_exporter/internal/app"
 "github.com/fjacquet/licenses_exporter/internal/config"
 "github.com/fsnotify/fsnotify"
 "github.com/sirupsen/logrus"
 "github.com/spf13/cobra"
)

// version is injected via -ldflags at build time (see Makefile `make cli`).
var version = "dev"

func main() {
 var (
  cfgPath string
  debug   bool
  once    bool
  trace   bool
  addr    string
 )
 root := &cobra.Command{
  Use:   "licenses_exporter",
  Short: "Unified enterprise-license Prometheus + OTLP exporter",
  RunE: func(cmd *cobra.Command, _ []string) error {
   if debug {
    logrus.SetLevel(logrus.DebugLevel)
   }
   if trace {
    logrus.Warn("--trace: both collectors use non-injectable SDKs; SDK-level tracing is intentionally not wired (would leak tokens). See docs/adr.")
   }
   cfg, err := config.Load(cfgPath)
   if err != nil {
    return err
   }
   if once {
    return app.Run(context.Background(), cfg, version, addr, true)
   }
   return serveWithReload(cfgPath, version, addr)
  },
 }
 root.Flags().StringVar(&cfgPath, "config", "config.yaml", "path to config.yaml")
 root.Flags().StringVar(&addr, "web.listen-address", ":9105", "metrics listen address")
 root.Flags().BoolVar(&debug, "debug", false, "debug logging")
 root.Flags().BoolVar(&once, "once", false, "run one collection cycle and exit")
 root.Flags().BoolVar(&trace, "trace", false, "log repo-owned API responses (SDK tracing intentionally disabled)")
 if err := root.Execute(); err != nil {
  logrus.WithError(err).Fatal("exporter failed")
 }
}

// serveWithReload runs the exporter under a cancelable context and rebuilds it on
// SIGHUP or config file change (design spec §2). A config that fails to load is
// rejected; the running server keeps serving.
func serveWithReload(cfgPath, version, addr string) error {
 sigs := make(chan os.Signal, 1)
 signal.Notify(sigs, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM)

 watcher, _ := fsnotify.NewWatcher()
 if watcher != nil {
  defer watcher.Close()
  _ = watcher.Add(cfgPath)
 }

 for {
  cfg, err := config.Load(cfgPath)
  if err != nil {
   return err // initial load failure is fatal
  }
  ctx, cancel := context.WithCancel(context.Background())
  go func() {
   if err := app.Run(ctx, cfg, version, addr, false); err != nil {
    logrus.WithError(err).Error("run cycle ended")
   }
  }()

  reload := false
  for !reload {
   select {
   case sig := <-sigs:
    if sig == syscall.SIGHUP {
     logrus.Info("SIGHUP: reloading config")
     reload = true
    } else {
     cancel()
     return nil // SIGINT/SIGTERM: shut down
    }
   case ev := <-watcherEvents(watcher):
    if ev.Op&(fsnotify.Write|fsnotify.Create) != 0 {
     logrus.WithField("file", ev.Name).Info("config changed: reloading")
     reload = true
    }
   }
  }
  // Validate the new config BEFORE tearing down the running server.
  if _, err := config.Load(cfgPath); err != nil {
   logrus.WithError(err).Warn("new config invalid; keeping current running config")
   continue
  }
  cancel() // tear down old; loop rebuilds
 }
}

func watcherEvents(w *fsnotify.Watcher) <-chan fsnotify.Event {
 if w == nil {
  return make(chan fsnotify.Event) // never fires
 }
 return w.Events
}
```

> **Note:** the reload path cancels the old server then rebuilds; because HTTP binds the same `addr`, `app.Run` must fully release the port on ctx cancel (it does — `srv.Shutdown`). If a bind race surfaces on rapid reloads, add a brief retry on `ListenAndServe` bind. Acceptable for v1; harden only if it surfaces.

- [ ] **Step 6: Build + smoke test**

Run:

```bash
go get github.com/fsnotify/fsnotify github.com/spf13/cobra
go mod tidy
go build -o bin/licenses_exporter .
./bin/licenses_exporter --help
```

Expected: build OK; `--help` lists `--config --debug --once --trace --web.listen-address`.

- [ ] **Step 7: Commit**

```bash
git add internal/app/ main.go go.mod go.sum
git commit -m "feat(app): main wiring — server-before-collect, /health, SIGHUP+file-watch reload, --once"
```

---

## Task 10: Makefile, Dockerfile, GoReleaser, tooling

**Files:**

- Create: `Makefile`, `Dockerfile`, `Dockerfile.goreleaser`, `.goreleaser.yaml`, `.golangci.yml` (`.gitignore` already created — verify)

**Approach:** copy the family boilerplate from `pflex_exporter` (hand-rolled sibling with the full target contract) and rename. Concrete, not a placeholder.

- [ ] **Step 1: Copy and adapt the Makefile**

Run:

```bash
cp /Users/fjacquet/Projects/pflex_exporter/Makefile ./Makefile
```

Edit `./Makefile`: replace every `pflex_exporter`/`pflex` with `licenses_exporter`. Verify the target contract:

```bash
grep -E '^(tools|fmt-check|fmt|vet|lint|test|test-race|test-coverage|vuln|ci|sure|cli|sbom|release|release-snapshot|docker|run-cli|clean):' Makefile | sort
```

Expected: all of `tools fmt-check fmt vet lint test test-race test-coverage vuln ci sure cli sbom release release-snapshot docker run-cli clean` present. Ensure `cli` injects version: `-ldflags "-X main.version=$(VERSION)"`.

- [ ] **Step 2: Copy and adapt the Dockerfiles + goreleaser + linter config**

Run:

```bash
cp /Users/fjacquet/Projects/pflex_exporter/Dockerfile ./Dockerfile
cp /Users/fjacquet/Projects/pflex_exporter/Dockerfile.goreleaser ./Dockerfile.goreleaser
cp /Users/fjacquet/Projects/pflex_exporter/.goreleaser.yaml ./.goreleaser.yaml
cp /Users/fjacquet/Projects/pflex_exporter/.golangci.yml ./.golangci.yml
```

Edit each: replace `pflex_exporter`/`pflex`→`licenses_exporter`, set `EXPOSE 9105` in `Dockerfile`. Confirm the CA-cert line is `COPY --from=builder /etc/ssl/certs/ca-certificates.crt ...` (NOT `apk add ca-certificates`) and a non-root `USER` (`adduser -D -u 10001`). In `.goreleaser.yaml` confirm `version: 2`, CGO off, linux/darwin × amd64/arm64, cyclonedx-gomod SBOM, checksums, self-skipping Homebrew cask; update project + binary names.

- [ ] **Step 3: Verify the gate runs**

Run:

```bash
make tools
make ci
```

Expected: `make ci` green — gofmt, vet, golangci-lint, `go test -race ./...`, govulncheck all pass. Fix any lint findings by restructuring (no `//nolint`).

- [ ] **Step 4: Commit**

```bash
git add Makefile Dockerfile Dockerfile.goreleaser .goreleaser.yaml .golangci.yml
git commit -m "build: family Makefile contract, non-root Dockerfiles, GoReleaser, golangci config"
```

---

## Task 11: CI/CD caller stubs + dependabot

**Files:**

- Create: `.github/workflows/ci.yml`, `security.yml`, `release.yml`, `docs.yml`, `.github/dependabot.yml`

**Approach:** the family consumes reusable workflows from `fjacquet/ci@v1`; each caller is ~12 lines. Copy templates and keep them thin (do NOT inline steps or SHA-pin — that lives in `fjacquet/ci`).

- [ ] **Step 1: Copy the four caller stubs**

Run:

```bash
mkdir -p .github/workflows
cp /Users/fjacquet/Projects/pflex_exporter/.github/workflows/ci.yml       .github/workflows/ci.yml
cp /Users/fjacquet/Projects/pflex_exporter/.github/workflows/security.yml .github/workflows/security.yml
cp /Users/fjacquet/Projects/pflex_exporter/.github/workflows/release.yml  .github/workflows/release.yml
cp /Users/fjacquet/Projects/pflex_exporter/.github/workflows/docs.yml     .github/workflows/docs.yml
```

Verify each `uses:` targets `fjacquet/ci/.github/workflows/{go-ci,go-security,go-release,docs-publish}.yml@v1` with only caller `permissions:` + `secrets:` passthrough (`CODECOV_TOKEN`, `HOMEBREW_TAP_GITHUB_TOKEN`). Confirm nothing hardcodes `pflex`.

- [ ] **Step 2: Copy and trim dependabot to gomod + docker only**

Run:

```bash
cp /Users/fjacquet/Projects/pflex_exporter/.github/dependabot.yml .github/dependabot.yml
```

Verify it lists **only** `gomod` and `docker` ecosystems (no `github-actions`).

- [ ] **Step 3: Commit**

```bash
git add .github/
git commit -m "ci: thin fjacquet/ci@v1 caller stubs (ci/security/release/docs) + dependabot"
```

---

## Task 12: Observability demo stack (compose + Prometheus + Grafana)

**Files:**

- Create: `docker-compose.yml`, `docker-compose.ghcr.yml`, `prometheus.yml`, `deploy/prometheus/license.rules.yml`, `grafana/provisioning/datasources/datasource.yml`, `grafana/provisioning/dashboards/dashboards.yml`, `grafana/dashboards/licenses-overview.json`, `.env.example`

- [ ] **Step 1: Copy the compose + provisioning skeleton from a sibling**

Run:

```bash
cp /Users/fjacquet/Projects/pflex_exporter/docker-compose.yml ./docker-compose.yml
cp /Users/fjacquet/Projects/pflex_exporter/docker-compose.ghcr.yml ./docker-compose.ghcr.yml
cp /Users/fjacquet/Projects/pflex_exporter/prometheus.yml ./prometheus.yml
mkdir -p deploy/prometheus grafana/provisioning/datasources grafana/provisioning/dashboards grafana/dashboards
cp /Users/fjacquet/Projects/pflex_exporter/grafana/provisioning/datasources/datasource.yml grafana/provisioning/datasources/datasource.yml
cp /Users/fjacquet/Projects/pflex_exporter/grafana/provisioning/dashboards/dashboards.yml grafana/provisioning/dashboards/dashboards.yml
```

Edit: service/image names `pflex`→`licenses_exporter`, scrape target port → **9105**, GHCR image → `ghcr.io/fjacquet/licenses_exporter:latest`. Set compose `environment:` to pass `M3651_*` and `VMWARE1_*` vars with literal defaults.

- [ ] **Step 2: Write the alert rules**

Create `deploy/prometheus/license.rules.yml`:

```yaml
groups:
  - name: licenses
    rules:
      - alert: LicenseOverAllocated
        expr: license_seats_used > license_seats_total
        for: 15m
        labels: { severity: warning }
        annotations:
          summary: "{{ $labels.vendor }}/{{ $labels.product }} over-allocated on {{ $labels.instance }}"
      - alert: LicenseExpiringSoon
        expr: license_expiration_timestamp_seconds - time() < 30 * 86400
        for: 1h
        labels: { severity: warning }
        annotations:
          summary: "{{ $labels.vendor }}/{{ $labels.product }} expires in < 30d"
      - alert: LicenseCollectorDown
        expr: license_up == 0
        for: 30m
        labels: { severity: critical }
        annotations:
          summary: "license collector down for {{ $labels.vendor }}/{{ $labels.instance }}"
```

Confirm `prometheus.yml` has a `rule_files:` entry pointing at the mounted rules path and a scrape job for `licenses_exporter:9105`.

- [ ] **Step 3: Build the Grafana dashboard JSON**

Create `grafana/dashboards/licenses-overview.json` — templating variables `vendor`, `instance`, `product` (each a `label_values(...)` query against the provisioned datasource) and these panels (each uses the provisioned datasource; no `rate()`):

| Panel (title) | `expr` |
|---|---|
| Seat utilization % | `100 * license_seats_used / license_seats_total` |
| Over-allocated licenses | `license_seats_used > license_seats_total` |
| Seats free | `license_seats_total - license_seats_used` |
| Days to expiration | `(license_expiration_timestamp_seconds - time()) / 86400` |
| Expiring < 30d | `license_expiration_timestamp_seconds - time() < 30*86400` |
| Collector health | `license_up{vendor=~"$vendor",instance=~"$instance"}` |
| Last refresh age (s) | `time() - license_collector_last_success_timestamp_seconds` |

Start from `pflex_exporter/grafana/dashboards/*.json` as a structural template (panel/target/templating JSON shape), replace panels/targets with the rows above, title "Enterprise Licenses — Overview". Validate: `python3 -m json.tool grafana/dashboards/licenses-overview.json >/dev/null`.

- [ ] **Step 4: Write `.env.example`**

Create `.env.example`:

```bash
# M365 (single-target quickstart; config.yaml is the source of truth)
M3651_TENANT_ID=
M3651_CLIENT_ID=
M3651_CLIENT_SECRET=
# VMware
VMWARE1_HOST=https://vcsa01/sdk
VMWARE1_USER=
VMWARE1_PASSWORD=
# Grafana
GRAFANA_ADMIN_PASSWORD=admin
```

- [ ] **Step 5: Verify the stack config**

Run:

```bash
docker compose config >/dev/null && echo "compose valid"
```

Expected: `compose valid` (full `docker compose up` needs real creds).

- [ ] **Step 6: Commit**

```bash
git add docker-compose.yml docker-compose.ghcr.yml prometheus.yml deploy/ grafana/ .env.example
git commit -m "deploy: docker-compose + Prometheus rules + Grafana licenses dashboard demo stack"
```

---

## Task 13: Docs & ADRs

**Files:**

- Create: `mkdocs.yml`, `docs/metrics.md`, `docs/deployment/docker.md`, `docs/dashboards.md`, `docs/adr/index.md`, `docs/adr/0001-*.md` … `0009-*.md`, `CHANGELOG.md`
- (`CLAUDE.md` already exists — verify it matches the final layout.)

- [ ] **Step 1: Write the metrics catalog**

Create `docs/metrics.md` documenting every metric from Task 1 (name, gauge, labels, meaning) — copy the schema table from design spec §4 plus the health metrics. This is the diff target for `--once --debug`.

- [ ] **Step 2: Write the ADRs**

Run:

```bash
mkdir -p docs/adr
cp /Users/fjacquet/Projects/ppdd_exporter/docs/adr/index.md docs/adr/index.md   # then rewrite the list
```

Author, as `docs/adr/NNNN-title.md` (Status/Context/Decision/Consequences), reusing sibling ADRs as templates:

- `0001-supply-chain-release-hardening.md` (mirror `pflex` 0001)
- `0002-prometheus-snapshot-model.md` (mirror `ppdd` 0001)
- `0003-client-choice-govmomi-sdk-and-msgraph-sdk.md` — **novel**: govmomi available+useful (single property-collector fetch, current session auth) → SDK; msgraph-sdk-go chosen despite its heavy dep tree as a *roadmap-justified exception* (phase-2 Entra ID amortization), `azidentity` for auth.
- `0004-generic-prefix-vendor-label-schema.md` — **novel**: single `license_` prefix + `vendor,product,unit,instance` labels.
- `0005-raw-facts-absent-not-zero-naming-units.md` — **novel**: no `days_to_expiration`/`compliance_status`; expiration as a Unix timestamp omitted when perpetual; unlimited (`Total<=0`) omits `seats_total`.
- `0006-label-key-consistency-invariant.md` (mirror `ppdd` 0006).
- `0007-token-auth-retry-policy.md` — stateless govmomi session per cycle; `azidentity` for M365; retry excludes 4xx; `--trace` never enables SDK debug.
- `0008-config-hot-reload.md` — cancelable context + last-good-snapshot continuity.
- `0009-otlp-observation-time-vs-snapshot-time.md` — records the review §2.6 resolution (observation-time points + freshness metric).

Update `docs/adr/index.md` to list all nine.

- [ ] **Step 3: MkDocs site + deployment/dashboard docs**

Run:

```bash
cp /Users/fjacquet/Projects/pflex_exporter/mkdocs.yml ./mkdocs.yml
```

Edit `mkdocs.yml`: site name, repo URL, nav entries for `metrics.md`, `dashboards.md`, `deployment/docker.md`, the ADRs. Write `docs/deployment/docker.md` (compose quickstart + the required **`Organization.Read.All`** Graph app-permission grant + a vCenter read-only role note) and `docs/dashboards.md` (the panel/query table from Task 12). Build strict:

```bash
uvx --with mkdocs-material --with pymdown-extensions mkdocs build --strict
```

Expected: builds with no warnings.

- [ ] **Step 4: Changelog + commit**

Create `CHANGELOG.md` with an `Unreleased` → v1 entry summarizing the exporter, both collectors, schema, and demo stack. Then:

```bash
git add mkdocs.yml docs/ CHANGELOG.md CLAUDE.md
git commit -m "docs: metrics catalog, nine ADRs, mkdocs site, deployment + dashboard docs, changelog"
```

---

## Self-Review (completed by plan author)

**1. Spec coverage** — each design-spec section maps to a task:

- §1 scope (M365 + VMware) → Tasks 7, 8. §2 snapshot/dual-export/cold-start/reload/OTLP → Tasks 2, 3, 4, 5, 9. §3 `Source` → Task 3. §4 schema/labels/absent-not-zero → Tasks 1, 7, 8. §5 config → Task 6. §6 clients/auth/stateless/unlimited/paging → Tasks 7, 8; ADR-0003 → Task 13. §7 `--once/--debug/--trace` → Task 9. §8 conformance (Makefile/Docker/CI/demo/Grafana) → Tasks 10, 11, 12; docs/ADRs → Task 13. §9 tests (vcsim, both export paths, label parity) → Tasks 4, 5, 7, 8. Review addendum 2.1–2.6 → Tasks 7 (2.1, 2.2), 9 (2.3, 2.4), 8 (2.5), 5 (2.6). **No gaps.**

**2. Placeholder scan** — every code step contains complete code and exact commands; no "TBD/handle edge cases/similar to Task N". The three confirm-at-impl notes (govmomi `Used` type, `expirationDate` value type, msgraph `NewPageIterator` signature) are explicit `go doc` verification steps, not placeholders — they exist because those signatures legitimately vary by SDK version.

**3. Type consistency** — `Sample{Name,Labels,Value}`, `Source{Vendor,Instance,Collect}`, `NewCollector(sources, store, version, goVersion, limit, now)`, `SnapshotStore.{Load,Swap}`, `NewSources(config.<Vendor>Raw)`, `licensesToSamples`/`skusToSamples`, and the `license_*` metric consts are used identically across Tasks 1–9. The govmomi/internal `license` package-name collision is resolved with the `vlicense` alias (Task 7).

---

## Execution Handoff

**Plan complete and saved to `docs/superpowers/plans/2026-07-01-licenses-exporter-v1.md`.**

Two execution options:

1. **Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration.
2. **Inline Execution** — execute tasks in this session using executing-plans, batch execution with checkpoints.

**Which approach?**
