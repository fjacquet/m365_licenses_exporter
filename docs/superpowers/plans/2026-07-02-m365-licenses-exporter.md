# m365_licenses_exporter Conversion Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Convert this repo from the unified `licenses_exporter` into `m365_licenses_exporter` — the first vendor exporter that consumes `licenses-exporter-core` v0.1.0, keeping only the Microsoft 365 collector.

**Architecture:** Delete the vendor-neutral engine (now provided by `github.com/fjacquet/licenses-exporter-core`) and the VMware collector. Keep `internal/m365` (Microsoft Graph via `msgraph-sdk-go`), rewired to build `core.Source`s and `core.Sample`s. A ~40-line `main.go` parses flags and hands `core.Main` an `App` whose `Load` re-parses config and calls `m365.NewSources`. The `license_` wire schema is unchanged because every sample is still built through core constructors.

**Tech Stack:** Go 1.26.x, `github.com/fjacquet/licenses-exporter-core` v0.1.0, `github.com/microsoftgraph/msgraph-sdk-go`, `github.com/Azure/azure-sdk-for-go/sdk/azidentity`, `github.com/spf13/cobra`, `github.com/sirupsen/logrus`.

## Global Constraints

- Module path becomes `github.com/fjacquet/m365_licenses_exporter` (was `github.com/fjacquet/licenses_exporter`).
- Depend on `github.com/fjacquet/licenses-exporter-core v0.1.0` — published, no `replace` directive.
- Keep `msgraph-sdk-go` for Graph access — the msgraph→resty rewrite is an explicit later PR, NOT part of this conversion.
- Remove `github.com/vmware/govmomi` entirely (VMware collector is deleted).
- Schema identity: build every `Sample` only through core constructors (`core.SeatSample`, `core.MetricSeatsTotal`, `core.MetricSeatsUsed`). Never a raw `core.Sample{}` literal in vendor code.
- Raw-facts / absent-never-zero: a nil/missing Graph count yields an **absent** sample, never a fake `0`; a SKU with empty `skuPartNumber` is **skipped** (ADR-0005). Preserve `internal/m365/parse.go`'s existing nil-guards byte-for-byte.
- Vendor label constant stays `vendor = "microsoft"`, `unit = "users"`.
- Secrets are `${ENV}` refs or `clientSecretFile` only (via `core.ResolveSecret`) — never hardcoded or logged.
- `--trace` never enables SDK debug modes (would leak the bearer token). It only warns.
- No inline `//nolint` / `# nosemgrep` suppressions, except the ratified `# nosemgrep` on CI caller `uses: fjacquet/ci/...@vN` lines.
- Family Makefile target contract (`make cli`, `make ci`, `make release-snapshot`), CI, GoReleaser, and docs (`docs/metrics.md` + `CHANGELOG.md`) updated in the same change.
- **Correctness oracle:** the existing `internal/m365/parse_test.go` and `source_test.go` must pass **unchanged** after the rewire (they reference no `internal/license`/`internal/config` symbols, so they compile as-is). Their passing is the byte-for-byte proof that m365 sample output is identical to the unified exporter's.
- Commit trailer on every commit:
  ```
  Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
  Claude-Session: https://claude.ai/code/session_01XksHzPMuvUvvkbPgLWXTN9
  ```

## File Structure

**Deleted** (engine now in core; VMware dropped):
- `internal/license/` (whole dir), `internal/app/` (whole dir), `internal/config/` (whole dir), `internal/vmware/` (whole dir)
- `main.go`'s `serveWithReload` + watcher helpers (moved to core in v0.1.0)

**Created:**
- `internal/m365/config.go` — `M365Config` + `TenantConfig` (moved from deleted `internal/config`).
- `config.go` (package `main`) — the consumer `Config` struct embedding `core.Base`.
- `main_test.go` (package `main`) — config-load + --once smoke.

**Modified:**
- `go.mod` / `go.sum` — module rename, add core, drop govmomi.
- `internal/m365/m365.go`, `internal/m365/source.go`, `internal/m365/parse.go` — import `core` instead of `internal/license`/`internal/config`.
- `main.go` — thin cobra wrapper delegating to `core.Main`.
- `Makefile`, `.goreleaser.yaml`, `Dockerfile`, `Dockerfile.goreleaser`, `docker-compose.yml`, `docker-compose.ghcr.yml`, `config.yaml`, `README.md`, `docs/metrics.md`, ADR index, `CHANGELOG.md` — rename + architecture note.

---

## Task 1: Rewire `internal/m365` to core; delete the engine

Make the repo a core consumer that compiles as a library (no binary yet). The m365 mock tests are the oracle — they must stay green through the rewire.

**Files:**
- Modify: `go.mod` (module path, requires)
- Create: `internal/m365/config.go`
- Modify: `internal/m365/m365.go`, `internal/m365/source.go`, `internal/m365/parse.go`
- Delete: `internal/license/`, `internal/app/`, `internal/config/`, `internal/vmware/`, `main.go`
- Test (unchanged): `internal/m365/parse_test.go`, `internal/m365/source_test.go`

**Interfaces:**
- Consumes from core v0.1.0: `core.Source` (interface: `Vendor() string`, `Instance() string`, `Collect(context.Context) ([]core.Sample, error)`); `core.Sample`; `core.SeatSample(name, vendor, product, unit, instance string, v float64) core.Sample`; `core.MetricSeatsTotal`, `core.MetricSeatsUsed` (string consts); `core.ResolveSecret(inline, file string) (string, error)`.
- Produces for Task 2: `m365.NewSources(cfg m365.M365Config) ([]core.Source, error)`; `m365.M365Config` (`Enabled bool`, `Tenants []m365.TenantConfig`); `m365.TenantConfig` (`Instance, TenantID, ClientID, ClientSecret, ClientSecretFile string`).

- [ ] **Step 1: Delete the engine + VMware packages and the old main**

```bash
cd /Users/fjacquet/Projects/licenses_exporter
git rm -r internal/license internal/app internal/config internal/vmware
git rm main.go
```

Expected: files staged for deletion. The tree will not build until Steps 2-6 land — that's expected; do not commit yet.

- [ ] **Step 2: Rename the module and swap deps in `go.mod`**

Edit `go.mod` line 1:

```
module github.com/fjacquet/m365_licenses_exporter
```

Add to the `require` block:

```
github.com/fjacquet/licenses-exporter-core v0.1.0
```

Remove the `github.com/vmware/govmomi v0.55.0` require line (and any govmomi-only indirect lines — `go mod tidy` in Step 6 will finalize). Keep `msgraph-sdk-go`, `msgraph-sdk-go-core`, `azidentity`, `cobra`, `logrus`.

- [ ] **Step 3: Create `internal/m365/config.go`** (vendor config, moved from the deleted `internal/config`)

```go
package m365

// M365Config is the Microsoft 365 block of the exporter config. Enabled=false
// (or an empty Tenants list) yields zero sources — the exporter then serves only
// license_build_info.
type M365Config struct {
	Enabled bool            `yaml:"enabled"`
	Tenants []TenantConfig  `yaml:"tenants"`
}

// TenantConfig is one Entra tenant / Graph app registration. ClientSecret is an
// inline ${ENV} ref; ClientSecretFile is a path read at load. Exactly one is used
// (ResolveSecret governs precedence).
type TenantConfig struct {
	Instance         string `yaml:"instance"`
	TenantID         string `yaml:"tenantId"`
	ClientID         string `yaml:"clientId"`
	ClientSecret     string `yaml:"clientSecret"`
	ClientSecretFile string `yaml:"clientSecretFile"`
}
```

- [ ] **Step 4: Rewire `internal/m365/parse.go`** (import path only; logic byte-identical)

Replace the import block and the two `license.` qualifiers with `core.`:

```go
package m365

import (
	core "github.com/fjacquet/licenses-exporter-core"
	"github.com/microsoftgraph/msgraph-sdk-go/models"
)

const (
	vendor = "microsoft"
	unit   = "users"
)

// skusToSamples maps subscribedSkus to license samples. Every getter is
// nil-guarded: a missing count yields an absent sample, never a fake 0.
func skusToSamples(instance string, skus []models.SubscribedSkuable) []core.Sample {
	var out []core.Sample
	for _, sku := range skus {
		if sku == nil {
			continue
		}
		// A SKU with no skuPartNumber cannot be identified; emitting product=""
		// would collapse distinct such SKUs onto one series. Skip it (absent, not
		// a blank-labelled fake) per the raw-facts contract (ADR-0005).
		p := sku.GetSkuPartNumber()
		if p == nil || *p == "" {
			continue
		}
		product := *p
		if pre := sku.GetPrepaidUnits(); pre != nil {
			if enabled := pre.GetEnabled(); enabled != nil {
				out = append(out, core.SeatSample(core.MetricSeatsTotal, vendor, product, unit, instance, float64(*enabled)))
			}
		}
		if consumed := sku.GetConsumedUnits(); consumed != nil {
			out = append(out, core.SeatSample(core.MetricSeatsUsed, vendor, product, unit, instance, float64(*consumed)))
		}
	}
	return out
}
```

- [ ] **Step 5: Rewire `internal/m365/source.go` and `internal/m365/m365.go`**

`source.go` — swap `license` → `core`, return `[]core.Sample`:

```go
package m365

import (
	"context"

	core "github.com/fjacquet/licenses-exporter-core"
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

func (s *source) Collect(ctx context.Context) ([]core.Sample, error) {
	skus, err := s.lister.listSkus(ctx)
	if err != nil {
		return nil, err
	}
	return skusToSamples(s.instance, skus), nil
}
```

`m365.go` — take `M365Config`, use `core.ResolveSecret`, return `[]core.Source`:

```go
package m365

import (
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	core "github.com/fjacquet/licenses-exporter-core"
	msgraphsdk "github.com/microsoftgraph/msgraph-sdk-go"
)

// graphScopes requests the app-only default scope; the app registration must be
// granted Organization.Read.All (or Directory.Read.All) — see docs/deployment.
var graphScopes = []string{"https://graph.microsoft.com/.default"}

// NewSources builds one core.Source per configured tenant.
func NewSources(cfg M365Config) ([]core.Source, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	var out []core.Source
	for _, t := range cfg.Tenants {
		secret, err := core.ResolveSecret(t.ClientSecret, t.ClientSecretFile)
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
```

> Note: `graphSkuLister` (the real Graph adapter) is defined in the existing `internal/m365/graph.go` and is unchanged — it depends only on the msgraph client, not on `license`/`config`. If a grep shows it imports `internal/license`, swap that qualifier to `core` too.

- [ ] **Step 6: Tidy and verify the library compiles + oracle tests pass**

Run:
```bash
cd /Users/fjacquet/Projects/licenses_exporter
GOFLAGS=-mod=mod go mod tidy
go build ./...
go test ./internal/m365/... -race -v
```
Expected: `go build ./...` succeeds (no binary — there is no `package main` yet). `go test ./internal/m365/...` PASS — `parse_test.go` and `source_test.go` pass **unchanged**, proving sample-output parity. `go mod tidy` leaves `govmomi` absent from `go.mod`/`go.sum` and `licenses-exporter-core v0.1.0` present.

- [ ] **Step 7: Confirm no stale references remain**

```bash
grep -rn 'internal/license\|internal/config\|internal/app\|internal/vmware\|govmomi' --include='*.go' .
```
Expected: no output (empty). If any line prints, fix that qualifier before committing.

- [ ] **Step 8: Commit**

```bash
git add -A
git commit -m "refactor: consume licenses-exporter-core v0.1.0; drop engine + VMware" -m "Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01XksHzPMuvUvvkbPgLWXTN9"
```

---

## Task 2: Thin `main.go` + consumer `Config` + config.yaml

Add the binary: a cobra wrapper that delegates the whole lifecycle to `core.Main`, plus the consumer `Config` that embeds `core.Base`. Restructure `config.yaml` to the m365-only shape.

**Files:**
- Create: `config.go` (package `main`), `main.go` (package `main`), `main_test.go` (package `main`)
- Modify: `config.yaml`

**Interfaces:**
- Consumes from core v0.1.0: `core.Base` (`Validate() error`; embeds `Collection` + `OTLP`, yaml-inline); `core.LoadYAML(path string, into any) error`; `core.Main(app core.App) error`; `core.App{ Version, Addr string; Once, Debug, Trace bool; ConfigPath string; Load func() (core.Base, []core.Source, error) }`; `core.Source`.
- Consumes from Task 1: `m365.NewSources(m365.M365Config) ([]core.Source, error)`, `m365.M365Config`.
- Produces for Task 3: binary at `bin/m365_licenses_exporter`; flags `--config` (default `config.yaml`), `--web.listen-address` (default `:9105`), `--debug`, `--once`, `--trace`.

- [ ] **Step 1: Write the failing test** `main_test.go`

```go
package main

import (
	"os"
	"path/filepath"
	"testing"

	core "github.com/fjacquet/licenses-exporter-core"
)

// TestLoadConfigParsesBaseAndM365 proves the consumer Config wires core.Base
// (collection/otlp) AND the vendor m365 block from one YAML file.
func TestLoadConfigParsesBaseAndM365(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yaml := `
collection:
  interval: 3h
otlp:
  endpoint: "otel:4317"
  insecure: true
m365:
  enabled: true
  tenants:
    - instance: tenant-a
      tenantId: t-id
      clientId: c-id
      clientSecret: shhh
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	var cfg Config
	if err := core.LoadYAML(path, &cfg); err != nil {
		t.Fatalf("LoadYAML: %v", err)
	}
	if cfg.Base.Collection.Interval.Hours() != 3 {
		t.Errorf("interval = %v, want 3h", cfg.Base.Collection.Interval)
	}
	if cfg.Base.OTLP.Endpoint != "otel:4317" {
		t.Errorf("otlp endpoint = %q, want otel:4317", cfg.Base.OTLP.Endpoint)
	}
	if !cfg.M365.Enabled || len(cfg.M365.Tenants) != 1 || cfg.M365.Tenants[0].Instance != "tenant-a" {
		t.Errorf("m365 block not parsed: %+v", cfg.M365)
	}
}

// TestLoadReturnsSourcesForEnabledTenant proves the App.Load closure builds a
// core.Source per enabled tenant (the wiring core.Main will drive).
func TestLoadReturnsSourcesForEnabledTenant(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yaml := `
collection:
  interval: 2h
m365:
  enabled: true
  tenants:
    - instance: tenant-a
      tenantId: t-id
      clientId: c-id
      clientSecret: shhh
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	base, sources, err := loadConfig(path)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if base.Collection.Interval.Hours() != 2 {
		t.Errorf("interval = %v, want 2h", base.Collection.Interval)
	}
	if len(sources) != 1 {
		t.Fatalf("got %d sources, want 1", len(sources))
	}
	if sources[0].Vendor() != "microsoft" || sources[0].Instance() != "tenant-a" {
		t.Errorf("source identity = %s/%s, want microsoft/tenant-a", sources[0].Vendor(), sources[0].Instance())
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test . -run TestLoad -v`
Expected: FAIL — `undefined: Config`, `undefined: loadConfig`.

- [ ] **Step 3: Create `config.go`** (consumer config + the shared load helper)

```go
package main

import (
	core "github.com/fjacquet/licenses-exporter-core"
	"github.com/fjacquet/m365_licenses_exporter/internal/m365"
)

// Config is the exporter's full config: the shared core.Base (collection + otlp)
// inline, plus the vendor-specific m365 block.
type Config struct {
	core.Base `yaml:",inline"`
	M365      m365.M365Config `yaml:"m365"`
}

// loadConfig parses the file and builds the sources — the single closure body
// core.Main calls at startup and on every reload.
func loadConfig(path string) (core.Base, []core.Source, error) {
	var cfg Config
	if err := core.LoadYAML(path, &cfg); err != nil {
		return core.Base{}, nil, err
	}
	if err := cfg.Base.Validate(); err != nil {
		return core.Base{}, nil, err
	}
	sources, err := m365.NewSources(cfg.M365)
	if err != nil {
		return core.Base{}, nil, err
	}
	return cfg.Base, sources, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test . -run TestLoad -v`
Expected: PASS (both).

- [ ] **Step 5: Create the thin `main.go`**

```go
package main

import (
	core "github.com/fjacquet/licenses-exporter-core"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

// version is injected via -ldflags at build time (see Makefile `make cli`).
var version = "dev"

func main() {
	var (
		cfgPath string
		addr    string
		debug   bool
		once    bool
		trace   bool
	)
	root := &cobra.Command{
		Use:   "m365_licenses_exporter",
		Short: "Microsoft 365 license Prometheus + OTLP exporter",
		RunE: func(_ *cobra.Command, _ []string) error {
			return core.Main(core.App{
				Version:    version,
				Addr:       addr,
				Once:       once,
				Debug:      debug,
				Trace:      trace,
				ConfigPath: cfgPath,
				Load:       func() (core.Base, []core.Source, error) { return loadConfig(cfgPath) },
			})
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
```

- [ ] **Step 6: Restructure `config.yaml`** to the m365-only shape (drop the `collectors:` wrapper and the `vmware:` block)

```yaml
# m365_licenses_exporter — Microsoft 365 license exporter.
# Secrets are ${ENV} refs (expanded at load) or clientSecretFile paths.

collection:
  interval: 2h            # how often to poll the Graph API

otlp:
  endpoint: ""            # empty disables OTLP; e.g. "otel-collector:4317"
  insecure: false

m365:
  enabled: true
  tenants:
    - instance: primary
      tenantId: ${M365_TENANT_ID}
      clientId: ${M365_CLIENT_ID}
      clientSecret: ${M365_CLIENT_SECRET}
      # clientSecretFile: /run/secrets/m365_client_secret  # alternative to clientSecret
```

- [ ] **Step 7: Build the binary and smoke it**

Run:
```bash
go build -ldflags="-s -w -X main.version=dev" -o bin/m365_licenses_exporter .
./bin/m365_licenses_exporter --help
printf 'collection:\n  interval: 2h\nm365:\n  enabled: false\n' > /tmp/m365-smoke.yaml
./bin/m365_licenses_exporter --once --config /tmp/m365-smoke.yaml
```
Expected: `--help` lists the five flags with `Use: m365_licenses_exporter`. The `--once` run with `enabled: false` exits 0 (zero sources → only `license_build_info` would be emitted; `--once` without `--debug` prints nothing and returns). No panic, no secret in output.

- [ ] **Step 8: Run the whole test suite**

Run: `go test ./... -race`
Expected: PASS (m365 oracle tests + the two new main tests).

- [ ] **Step 9: Commit**

```bash
git add -A
git commit -m "feat: thin main delegating to core.Main; m365-only config.yaml" -m "Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01XksHzPMuvUvvkbPgLWXTN9"
```

---

## Task 3: Rename scaffolding + docs; add the core-dependency ADR

Rename `licenses_exporter` → `m365_licenses_exporter` across build/release/deploy files and docs, and record the architecture change (consuming core) as an ADR. This is the family-conformance + docs gate.

**Files:**
- Modify: `Makefile` (BIN), `.goreleaser.yaml`, `Dockerfile`, `Dockerfile.goreleaser`, `docker-compose.yml`, `docker-compose.ghcr.yml`
- Modify: `README.md`, `docs/metrics.md`, `mkdocs.yml` (site_name/nav if named), the ADR index
- Create: `docs/adr/0010-consume-licenses-exporter-core.md` (or the next free ADR number)
- Modify: `CHANGELOG.md`

**Interfaces:**
- Consumes: the working binary + module from Tasks 1-2.
- Produces: green `make ci`, green `make release-snapshot`, strict mkdocs build.

- [ ] **Step 1: Rename the binary in `Makefile`**

Change line 3:
```make
BIN     = m365_licenses_exporter
```
Leave `LDFLAGS = -s -w -X main.version=$(VERSION)` — `main.version` still exists in the new `main.go`. Confirm `cli:` still reads `go build -ldflags="$(LDFLAGS)" -o bin/$(BIN) .` and `run:` reads `./bin/$(BIN) --config config.yaml`.

- [ ] **Step 2: Rename in release/deploy files**

In `.goreleaser.yaml`, `Dockerfile`, `Dockerfile.goreleaser`, `docker-compose.yml`, `docker-compose.ghcr.yml`: replace every `licenses_exporter` with `m365_licenses_exporter` (project name, binary name, image name/tags, service name, container name, mounted config path). Verify none was missed:
```bash
grep -rn 'licenses_exporter' Makefile .goreleaser.yaml Dockerfile Dockerfile.goreleaser docker-compose.yml docker-compose.ghcr.yml
```
Expected after edits: every remaining hit is intentionally `m365_licenses_exporter` (grep for the bare old name `grep -rn 'fjacquet/licenses_exporter\b'` should be empty except historical CHANGELOG lines).

- [ ] **Step 3: Update `README.md`**

Retitle to `# m365_licenses_exporter`; state it is a Microsoft-365 license exporter built on `github.com/fjacquet/licenses-exporter-core`; drop VMware/vSphere prose; keep the `license_` schema description, the Entra app-registration setup (Graph `Organization.Read.All`), and the run/compose instructions (with the new binary name). Add a one-line "Part of the licenses_exporter family; shares the `license_` schema via licenses-exporter-core" note.

- [ ] **Step 4: Update `docs/metrics.md`**

Remove VMware-specific rows/examples; keep the generic `license_` schema, the m365 examples (`vendor="microsoft"`, `product="SPE_E5"`, `unit="users"`), and `license_up`/`license_build_info`. Confirm the metric names match core exactly (`license_seats_total`, `license_seats_used`, `license_expiration_timestamp_seconds`, `license_up`, `license_collector_last_success_timestamp_seconds`, `license_scrape_duration_seconds`, `license_build_info`).

- [ ] **Step 5: Add the ADR for consuming core**

Create `docs/adr/0010-consume-licenses-exporter-core.md` (use the next free number if 0010 exists):

```markdown
# 10. Consume licenses-exporter-core instead of an in-repo engine

Date: 2026-07-02

## Status
Accepted

## Context
The unified exporter's vendor-neutral engine (schema, snapshot store, collection
loop, dual export, hot-reload server) was extracted into the reusable library
`github.com/fjacquet/licenses-exporter-core` v0.1.0. Keeping a private copy here
would let the `license_` schema drift between per-vendor exporters — the exact
outcome the split exists to prevent.

## Decision
This exporter depends on `licenses-exporter-core` and builds every sample through
its constructors. `main.go` delegates the whole lifecycle to `core.Main`; the repo
owns only `internal/m365` (the Graph collector) and the consumer `Config`.

## Consequences
- Schema identity is guaranteed by construction — no local `license_` metric code.
- Build time drops (no vSphere/govmomi tree; the engine compiles once, upstream).
- Engine bugfixes/features arrive via a core version bump, not a local edit.
- A core API change can require a coordinated bump here (acceptable during core's
  0.x settling window).
- Startup is now fatal on an unbuildable-but-valid config (core behaviour); see the
  core CHANGELOG.
```

Add a one-line entry to `docs/adr/index.md` (the ADR index — highest existing is 0009, so `0010` is the correct free number) and to `mkdocs.yml` nav if ADRs are listed there.

- [ ] **Step 6: Update `CHANGELOG.md`**

Add a new top entry:

```markdown
## [Unreleased]

### Changed
- **Repo split:** `licenses_exporter` becomes `m365_licenses_exporter` — the first
  consumer of `github.com/fjacquet/licenses-exporter-core` v0.1.0. The vendor-neutral
  engine and the VMware collector are removed; only the Microsoft 365 collector
  remains. The `license_` wire schema is unchanged (built via core constructors), so
  existing dashboards and alert rules keep working. See ADR-0010.
- Module path is now `github.com/fjacquet/m365_licenses_exporter`.
```

- [ ] **Step 7: Run the full CI gate + release dry-run + docs build**

Run:
```bash
make ci
make release-snapshot
uvx --with mkdocs-material --with pymdown-extensions mkdocs build --strict
```
Expected: `make ci` green (gofmt clean, vet, lint 0 issues, race tests pass, govulncheck clean, binary builds as `bin/m365_licenses_exporter`). `make release-snapshot` produces archives/SBOM/checksums named `m365_licenses_exporter`. mkdocs `--strict` build succeeds with no warnings.

- [ ] **Step 8: Semgrep scan (security gate)**

Run: `uvx semgrep scan --config auto --skip-unknown-extensions .`
Expected: 0 findings (no hardcoded secrets, no unsafe patterns introduced).

- [ ] **Step 9: Commit**

```bash
git add -A
git commit -m "chore: rename to m365_licenses_exporter across build/release/docs (+ADR-0010)" -m "Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01XksHzPMuvUvvkbPgLWXTN9"
```

---

## Task 4: Release (ops — run after the whole-branch review passes)

Not a TDD task. After the branch is merged to `main`, rename the GitHub repo and cut the first m365-line release. Confirm the tag version with the user before pushing.

- [ ] **Step 1: Rename the GitHub repo** (GitHub auto-redirects the old URL)

```bash
gh repo rename m365_licenses_exporter --repo fjacquet/licenses_exporter
git remote set-url origin https://github.com/fjacquet/m365_licenses_exporter.git
```

- [ ] **Step 2: Push and tag**

```bash
git push origin main
git tag -a v1.0.0 -m "m365_licenses_exporter v1.0.0 — core v0.1.0 consumer; M365-only"
git push origin v1.0.0
```

> Version note: `v1.0.0` restarts under the new module path (a new module identity). If the user prefers to preserve continuity from the unified `v1.0.0`, use `v1.1.0` instead — confirm before tagging.

- [ ] **Step 3: Verify the release** — `gh release view` shows the GoReleaser assets (binaries, archives, SBOM, checksums) named `m365_licenses_exporter`.

---

## Self-Review

**1. Spec coverage** (against the core design spec's "follow-on: m365 conversion" notes):
- Import core v0.1.0, keep msgraph, drop VMware/engine → Task 1. ✅
- Thin main via `core.Main` + `Config` embedding `core.Base` → Task 2. ✅
- Correctness oracle (golden m365 --once / sample parity) → Task 1 Step 6 (existing mock tests pass unchanged) + Task 2 Step 7 (--once smoke). ✅
- Repo rename + module path + version → Global Constraints + Task 1 Step 2 + Task 4. ✅
- Docs (metrics.md) + CHANGELOG + ADR in the same change → Task 3. ✅
- Family Makefile/CI/GoReleaser conformance → Task 3 Steps 1-2, 7. ✅

**2. Placeholder scan:** No "TBD"/"handle errors"/"similar to". Every code step shows complete code. The only conditional ("if graph.go imports license") is a concrete grep-and-swap instruction, not a placeholder.

**3. Type consistency:** `M365Config`/`TenantConfig` field names (`Enabled`, `Tenants`, `Instance`, `TenantID`, `ClientID`, `ClientSecret`, `ClientSecretFile`) match between Task 1 (definition) and Task 2 (`loadConfig`, test YAML). `core.SeatSample(name, vendor, product, unit, instance, v)`, `core.MetricSeatsTotal/Used`, `core.ResolveSecret`, `core.Base`, `core.LoadYAML`, `core.Main`, `core.App`, `core.Source`, `core.Sample` all match the published v0.1.0 surface. `loadConfig` signature `(path string) (core.Base, []core.Source, error)` matches the `App.Load` field type and the test. `vendor = "microsoft"`, `unit = "users"` unchanged.
