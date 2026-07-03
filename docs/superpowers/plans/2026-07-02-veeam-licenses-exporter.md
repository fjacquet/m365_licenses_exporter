# veeam_licenses_exporter Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `veeam_licenses_exporter` — a standalone Veeam license exporter that consumes `licenses-exporter-core` and reads license data from the Veeam Backup Enterprise Manager REST API via a hand-rolled resty client.

**Architecture:** A new repo mirroring `vmware_licenses_exporter`: thin `main.go` → `core.Main`; `Config` embeds `core.Base` + a `veeam:` block; the repo owns only `internal/veeam` (a hand-rolled resty client to Enterprise Manager `:9398`, session auth → `GET /api/licensing` → tolerant parse → `[]core.Sample`). Every sample is built via core constructors, so the `license_` schema matches the family.

**Tech Stack:** Go 1.26.x, `github.com/fjacquet/licenses-exporter-core` v1.0.0, `github.com/go-resty/resty/v2`, `github.com/spf13/cobra`, `github.com/sirupsen/logrus`; `net/http/httptest` for tests.

## Global Constraints

- New repo at `~/Projects/veeam_licenses_exporter`; module `github.com/fjacquet/veeam_licenses_exporter`; Go 1.26.x.
- Depend on `github.com/fjacquet/licenses-exporter-core v1.0.0` — published, NO `replace`.
- **Hand-rolled `resty/v2` client** — no Veeam SDK (none official; the unofficial one lacks licensing). Matches the family's `nbu_exporter`.
- Target the **Veeam Backup Enterprise Manager REST API** (`https://<host>:9398/api`): session auth (`POST /api/sessionMngr/?v=latest` Basic → `X-RestSvcSessionId`; `DELETE` session on logout — stateless per cycle), then `GET /api/licensing` with `Accept: application/json`.
- Schema identity: build every `Sample` only via core constructors (`core.SeatSample`, `core.ExpirationSample`, `core.MetricSeatsTotal`, `core.MetricSeatsUsed`) — never a raw `core.Sample{}` literal.
- Raw-facts / absent-never-zero: `seats_total` omitted when licensed instances `<= 0` (unlimited); `seats_used` always when present; `license_expiration_timestamp_seconds` from `ExpirationDate`, absent for perpetual / unparseable. `vendor="veeam"`, `unit="instances"`, `product`=license edition (fallback constant `"veeam"` if absent).
- **JSON model isolated** in `internal/veeam/model.go`; parser tolerant. The exact EM field names are unverified against a live instance → **first release `v0.1.0`** (settling window), documented in README + CHANGELOG.
- Secrets via `${ENV}`/`passwordFile` through `core.ResolveSecret`; never hardcoded or logged. `--trace` must never log the `X-RestSvcSessionId` header or the Basic credential.
- A source failure degrades to `license_up{vendor="veeam",instance}=0` (core guarantees this). Retry excludes 4xx.
- Default metrics port **9107** (m365=9105, vmware=9106, veeam=9107).
- No inline `//nolint`/`# nosemgrep` except the ratified `# nosemgrep` on CI caller `uses:` lines.
- Family Makefile contract, CI, GoReleaser, docs (`docs/metrics.md`) + `CHANGELOG.md` + ADR-0001.
- Commit trailer on every commit:
  ```
  Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
  Claude-Session: https://claude.ai/code/session_01XksHzPMuvUvvkbPgLWXTN9
  ```

**Scaffolding template:** the `vmware_licenses_exporter` repo at `~/Projects/vmware_licenses_exporter` (a clean core-consumer) — read files via `git -C ~/Projects/vmware_licenses_exporter show HEAD:<path>`, rename `vmware`→`veeam`, `9106`→`9107`, govmomi→resty.

## File Structure

- `internal/veeam/config.go` — `VeeamConfig` + `ServerConfig`
- `internal/veeam/model.go` — the EM `/api/licensing` JSON model (isolated for later field verification)
- `internal/veeam/parse.go` — `parseLicense(raw []byte, instance string) ([]core.Sample, error)` (pure, tolerant)
- `internal/veeam/client.go` — `emClient` (resty): `login`, `licensing`, `logout`
- `internal/veeam/source.go` — `source` implementing `core.Source` (login → licensing → logout → parse)
- `internal/veeam/veeam.go` — `NewSources(VeeamConfig) ([]core.Source, error)`
- `config.go`, `main.go`, `config.yaml`, `main_test.go` (package `main`)
- family scaffolding + docs (Task 4)

---

## Task 1: Bootstrap repo + config + license model + tolerant parser

Stand up the module and the pure license-parsing logic (no HTTP yet). TDD the parser against canned JSON.

**Files:**
- Create: `~/Projects/veeam_licenses_exporter/go.mod`, `.gitignore`
- Create: `internal/veeam/config.go`, `internal/veeam/model.go`, `internal/veeam/parse.go`
- Test: `internal/veeam/parse_test.go`

**Interfaces:**
- Consumes core v1.0.0: `core.Sample`; `core.SeatSample(name, vendor, product, unit, instance string, v float64) core.Sample`; `core.ExpirationSample(vendor, product, instance string, tsUnix float64) core.Sample`; consts `core.MetricSeatsTotal`, `core.MetricSeatsUsed`.
- Produces for later tasks: `parseLicense(raw []byte, instance string) ([]core.Sample, error)`; `licenseInfo` model; `VeeamConfig{Enabled bool; Servers []ServerConfig}`; `ServerConfig{Instance, Host, Username, Password, PasswordFile string; InsecureSkipVerify bool}`.

- [ ] **Step 1: Init repo + module**

```bash
mkdir -p ~/Projects/veeam_licenses_exporter && cd ~/Projects/veeam_licenses_exporter
git init
go mod init github.com/fjacquet/veeam_licenses_exporter
go get github.com/fjacquet/licenses-exporter-core@v1.0.0
printf 'bin/\ndist/\n*.out\ncoverage.html\n.env\n.superpowers/\nsite/\n' > .gitignore
```

- [ ] **Step 2: Create `internal/veeam/config.go`**

```go
package veeam

// VeeamConfig is the Veeam block of the exporter config. Enabled=false (or an
// empty Servers list) yields zero sources — the exporter serves only
// license_build_info. Each server is a Veeam Backup Enterprise Manager host.
type VeeamConfig struct {
	Enabled bool           `yaml:"enabled"`
	Servers []ServerConfig `yaml:"servers"`
}

// ServerConfig is one Enterprise Manager target. Password is an inline ${ENV}
// ref; PasswordFile is a path read at load (ResolveSecret governs precedence).
type ServerConfig struct {
	Instance           string `yaml:"instance"`
	Host               string `yaml:"host"` // https://em-host:9398
	Username           string `yaml:"username"`
	Password           string `yaml:"password"`
	PasswordFile       string `yaml:"passwordFile"`
	InsecureSkipVerify bool   `yaml:"insecureSkipVerify"`
}
```

- [ ] **Step 3: Create `internal/veeam/model.go`** (isolated — field names to be verified against a live EM)

```go
package veeam

// licenseInfo is the subset of the Veeam Enterprise Manager GET /api/licensing
// response we consume. Field names/json tags are the documented Veeam licensing
// terms; they are NOT yet verified against a live Enterprise Manager, so this file
// is deliberately isolated — correcting a tag here must not require touching the
// parser. Absent fields decode to their zero value and are handled by the parser
// (absent-not-zero).
type licenseInfo struct {
	Edition                 string `json:"Edition"`
	Status                  string `json:"Status"`
	ExpirationDate          string `json:"ExpirationDate"` // RFC3339, e.g. "2027-01-31T00:00:00Z"
	LicensedInstancesNumber int    `json:"LicensedInstancesNumber"`
	UsedInstancesNumber     int    `json:"UsedInstancesNumber"`
}
```

- [ ] **Step 4: Write the failing test** `internal/veeam/parse_test.go`

```go
package veeam

import "testing"

func find(samples []core.Sample, name string) (core.Sample, bool) {
	for _, s := range samples {
		if s.Name == name {
			return s, true
		}
	}
	return core.Sample{}, false
}

// TestParseLimitedLicense: a limited, dated license emits seats_total, seats_used,
// and expiration, all labelled vendor=veeam / unit=instances / product=<Edition>.
func TestParseLimitedLicense(t *testing.T) {
	raw := []byte(`{"Edition":"Enterprise Plus","Status":"Valid","ExpirationDate":"2027-01-31T00:00:00Z","LicensedInstancesNumber":100,"UsedInstancesNumber":42}`)
	samples, err := parseLicense(raw, "em-a")
	if err != nil {
		t.Fatalf("parseLicense: %v", err)
	}
	total, ok := find(samples, core.MetricSeatsTotal)
	if !ok || total.Value != 100 {
		t.Fatalf("seats_total = %+v ok=%v, want 100", total, ok)
	}
	used, ok := find(samples, core.MetricSeatsUsed)
	if !ok || used.Value != 42 {
		t.Fatalf("seats_used = %+v ok=%v, want 42", used, ok)
	}
	exp, ok := find(samples, core.MetricExpiration)
	if !ok || exp.Value != 1801353600 { // 2027-01-31T00:00:00Z
		t.Fatalf("expiration = %+v ok=%v, want 1801353600", exp, ok)
	}
	// label check: vendor/unit/product on seats_total
	got := map[string]string{}
	for _, l := range total.Labels {
		got[l.Key] = l.Value
	}
	if got["vendor"] != "veeam" || got["unit"] != "instances" || got["product"] != "Enterprise Plus" || got["instance"] != "em-a" {
		t.Fatalf("labels = %v, want veeam/instances/Enterprise Plus/em-a", got)
	}
}

// TestParseUnlimitedOmitsTotal: LicensedInstancesNumber <= 0 omits seats_total.
func TestParseUnlimitedOmitsTotal(t *testing.T) {
	raw := []byte(`{"Edition":"Community","ExpirationDate":"2027-01-31T00:00:00Z","LicensedInstancesNumber":0,"UsedInstancesNumber":3}`)
	samples, err := parseLicense(raw, "em-a")
	if err != nil {
		t.Fatalf("parseLicense: %v", err)
	}
	if _, ok := find(samples, core.MetricSeatsTotal); ok {
		t.Fatal("seats_total must be omitted when LicensedInstancesNumber<=0")
	}
	if used, ok := find(samples, core.MetricSeatsUsed); !ok || used.Value != 3 {
		t.Fatalf("seats_used = %+v ok=%v, want 3", used, ok)
	}
}

// TestParsePerpetualOmitsExpiration: empty/absent ExpirationDate omits the
// expiration sample (perpetual), never emits a fake value.
func TestParsePerpetualOmitsExpiration(t *testing.T) {
	raw := []byte(`{"Edition":"Enterprise","LicensedInstancesNumber":50,"UsedInstancesNumber":10}`)
	samples, err := parseLicense(raw, "em-a")
	if err != nil {
		t.Fatalf("parseLicense: %v", err)
	}
	if _, ok := find(samples, core.MetricExpiration); ok {
		t.Fatal("expiration must be omitted when ExpirationDate is absent")
	}
}

// TestParseInvalidJSON: malformed body is an error (source degrades to up=0).
func TestParseInvalidJSON(t *testing.T) {
	if _, err := parseLicense([]byte(`not json`), "em-a"); err == nil {
		t.Fatal("expected error on malformed JSON")
	}
}
```
Add the import `core "github.com/fjacquet/licenses-exporter-core"` to the test file.

- [ ] **Step 5: Run tests to verify they fail**

Run: `go test ./internal/veeam/... -run TestParse -v`
Expected: FAIL — `undefined: parseLicense` (and `core.MetricExpiration` must exist; it does in core v1.0.0).

- [ ] **Step 6: Create `internal/veeam/parse.go`**

```go
package veeam

import (
	"encoding/json"
	"fmt"
	"time"

	core "github.com/fjacquet/licenses-exporter-core"
)

const (
	vendor = "veeam"
	unit   = "instances"
)

// parseLicense maps an Enterprise Manager /api/licensing response to license
// samples. Tolerant / absent-not-zero: unlimited (LicensedInstancesNumber<=0)
// omits seats_total; perpetual / unparseable ExpirationDate omits the expiration
// sample. A malformed body is an error so the source degrades to license_up=0.
func parseLicense(raw []byte, instance string) ([]core.Sample, error) {
	var li licenseInfo
	if err := json.Unmarshal(raw, &li); err != nil {
		return nil, fmt.Errorf("decode licensing response: %w", err)
	}
	product := li.Edition
	if product == "" {
		product = vendor
	}
	var out []core.Sample
	if li.LicensedInstancesNumber > 0 {
		out = append(out, core.SeatSample(core.MetricSeatsTotal, vendor, product, unit, instance, float64(li.LicensedInstancesNumber)))
	}
	out = append(out, core.SeatSample(core.MetricSeatsUsed, vendor, product, unit, instance, float64(li.UsedInstancesNumber)))
	if li.ExpirationDate != "" {
		if t, err := time.Parse(time.RFC3339, li.ExpirationDate); err == nil {
			out = append(out, core.ExpirationSample(vendor, product, instance, float64(t.Unix())))
		}
	}
	return out, nil
}
```

- [ ] **Step 7: Tidy, run tests, verify pass**

```bash
GOFLAGS=-mod=mod go mod tidy
go test ./internal/veeam/... -run TestParse -race -v
go build ./...
```
Expected: 4 parse tests PASS; `go build ./...` clean (no binary yet). `go.mod` has `licenses-exporter-core v1.0.0`.

- [ ] **Step 8: Commit**

```bash
git add -A
git commit -m "feat: Veeam EM license model + tolerant parser on core v1.0.0" -m "Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01XksHzPMuvUvvkbPgLWXTN9"
```

---

## Task 2: Enterprise Manager resty client + Source

Add the hand-rolled EM REST client (session auth → licensing → logout) and the `core.Source` that drives it. TDD against an `httptest` server (the oracle) — no live Veeam.

**Files:**
- Create: `internal/veeam/client.go`, `internal/veeam/source.go`, `internal/veeam/veeam.go`
- Test: `internal/veeam/source_test.go`

**Interfaces:**
- Consumes from Task 1: `parseLicense(raw []byte, instance string) ([]core.Sample, error)`; `VeeamConfig`/`ServerConfig`; `vendor` const.
- Consumes core v1.0.0: `core.Source`, `core.Sample`, `core.ResolveSecret(inline, file string) (string, error)`.
- Produces for Task 3: `NewSources(cfg VeeamConfig) ([]core.Source, error)`.

- [ ] **Step 1: Write the failing test** `internal/veeam/source_test.go`

```go
package veeam

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakeEM stands up an Enterprise Manager the client can talk to: a session login
// that returns the X-RestSvcSessionId header, a licensing endpoint, and a logout.
func fakeEM(t *testing.T, licenseJSON string) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/sessionMngr/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if _, _, ok := r.BasicAuth(); !ok {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("X-RestSvcSessionId", "sess-123")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"SessionId":"sess-123"}`))
	})
	mux.HandleFunc("/api/licensing", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-RestSvcSessionId") != "sess-123" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(licenseJSON))
	})
	mux.HandleFunc("/api/logonSessions/", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	return httptest.NewServer(mux)
}

func TestSourceCollectAgainstFakeEM(t *testing.T) {
	srv := fakeEM(t, `{"Edition":"Enterprise Plus","ExpirationDate":"2027-01-31T00:00:00Z","LicensedInstancesNumber":100,"UsedInstancesNumber":42}`)
	defer srv.Close()

	src := &source{instance: "em-a", host: srv.URL, username: "u", password: "p", insecure: true}
	samples, err := src.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if _, ok := find(samples, core.MetricSeatsUsed); !ok {
		t.Fatal("expected seats_used from fake EM")
	}
	if total, ok := find(samples, core.MetricSeatsTotal); !ok || total.Value != 100 {
		t.Fatalf("seats_total = %+v ok=%v, want 100", total, ok)
	}
}

// A 401 on licensing (bad session) surfaces as an error so the engine sets up=0.
func TestSourceCollectAuthFailure(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/sessionMngr/", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	src := &source{instance: "em-a", host: srv.URL, username: "u", password: "p", insecure: true}
	if _, err := src.Collect(context.Background()); err == nil {
		t.Fatal("expected error when session login fails")
	}
}

// NewSources builds one source per enabled server; disabled yields none.
func TestNewSources(t *testing.T) {
	got, err := NewSources(VeeamConfig{Enabled: true, Servers: []ServerConfig{{Instance: "em-a", Host: "https://em:9398", Username: "u", Password: "p"}}})
	if err != nil {
		t.Fatalf("NewSources: %v", err)
	}
	if len(got) != 1 || got[0].Vendor() != "veeam" || got[0].Instance() != "em-a" {
		t.Fatalf("sources = %+v, want one veeam/em-a", got)
	}
	none, err := NewSources(VeeamConfig{Enabled: false})
	if err != nil || none != nil {
		t.Fatalf("disabled NewSources = %v, %v; want nil,nil", none, err)
	}
}
```
Add the import `core "github.com/fjacquet/licenses-exporter-core"` where referenced.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/veeam/... -run 'TestSource|TestNewSources' -v`
Expected: FAIL — `undefined: source`, `undefined: NewSources`.

- [ ] **Step 3: Create `internal/veeam/client.go`**

```go
package veeam

import (
	"context"
	"crypto/tls"
	"fmt"
	"time"

	"github.com/go-resty/resty/v2"
)

// emClient is a hand-rolled Veeam Backup Enterprise Manager REST client. Session
// auth is stateless per cycle: login returns the X-RestSvcSessionId token, which
// is sent on the licensing GET, and the session is deleted on logout.
type emClient struct {
	rc    *resty.Client
	token string
	sid   string
}

func newEMClient(host string, insecure bool) *emClient {
	rc := resty.New().
		SetBaseURL(host).
		SetTimeout(30 * time.Second).
		SetTLSClientConfig(&tls.Config{InsecureSkipVerify: insecure}). // #nosec G402 — operator opt-in per server for self-signed EM certs
		SetHeader("Accept", "application/json").
		// Retry transport/5xx only; 4xx (auth) is never retried.
		SetRetryCount(2).
		AddRetryCondition(func(r *resty.Response, err error) bool {
			return err != nil || r.StatusCode() >= 500
		})
	return &emClient{rc: rc}
}

func (c *emClient) login(ctx context.Context, username, password string) error {
	resp, err := c.rc.R().SetContext(ctx).SetBasicAuth(username, password).
		Post("/api/sessionMngr/?v=latest")
	if err != nil {
		return fmt.Errorf("em login: %w", err)
	}
	if resp.StatusCode() >= 400 {
		return fmt.Errorf("em login: status %d", resp.StatusCode())
	}
	c.token = resp.Header().Get("X-RestSvcSessionId")
	if c.token == "" {
		return fmt.Errorf("em login: no X-RestSvcSessionId in response")
	}
	return nil
}

func (c *emClient) licensing(ctx context.Context) ([]byte, error) {
	resp, err := c.rc.R().SetContext(ctx).SetHeader("X-RestSvcSessionId", c.token).
		Get("/api/licensing")
	if err != nil {
		return nil, fmt.Errorf("em licensing: %w", err)
	}
	if resp.StatusCode() >= 400 {
		return nil, fmt.Errorf("em licensing: status %d", resp.StatusCode())
	}
	return resp.Body(), nil
}

// logout is best-effort on a fresh bounded context so it runs even if the cycle
// context was cancelled. The session id form is EM-version dependent; a failure is
// the caller's to log (potential session leak), never fatal.
func (c *emClient) logout() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := c.rc.R().SetContext(ctx).SetHeader("X-RestSvcSessionId", c.token).
		Delete("/api/logonSessions/current")
	return err
}
```

> Note (`# nosec G402`): the ONLY permitted inline suppression style in this family is the ratified `# nosemgrep` on CI lines. For gosec G402 here, do NOT use an inline `//nosec`; instead rely on the `.golangci.yml` path-scoped exclusion added in Task 4 Step 1. Remove the `// #nosec` comment from the code above — it is shown only to mark the line; the real suppression is config-scoped. If `make ci` in Task 4 still flags G402, add the path exclusion for `internal/veeam/client.go` there.

- [ ] **Step 4: Create `internal/veeam/source.go`**

```go
package veeam

import (
	"context"

	core "github.com/fjacquet/licenses-exporter-core"
	"github.com/sirupsen/logrus"
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

// Collect logs into Enterprise Manager, reads the licensing resource, logs out
// (best-effort), and parses the result — stateless per cycle. A logout failure is
// warned, not fatal, so operators see potential EM session leaks.
func (s *source) Collect(ctx context.Context) ([]core.Sample, error) {
	c := newEMClient(s.host, s.insecure)
	if err := c.login(ctx, s.username, s.password); err != nil {
		return nil, err
	}
	defer func() {
		if err := c.logout(); err != nil {
			logrus.WithFields(logrus.Fields{"vendor": vendor, "instance": s.instance}).WithError(err).Warn("veeam EM logout failed")
		}
	}()
	raw, err := c.licensing(ctx)
	if err != nil {
		return nil, err
	}
	return parseLicense(raw, s.instance)
}
```

- [ ] **Step 5: Create `internal/veeam/veeam.go`**

```go
package veeam

import (
	"fmt"

	core "github.com/fjacquet/licenses-exporter-core"
)

// NewSources builds one Source per configured Enterprise Manager server.
func NewSources(cfg VeeamConfig) ([]core.Source, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	var out []core.Source
	for _, s := range cfg.Servers {
		pw, err := core.ResolveSecret(s.Password, s.PasswordFile)
		if err != nil {
			return nil, fmt.Errorf("veeam server %q: %w", s.Instance, err)
		}
		out = append(out, &source{
			instance: s.Instance,
			host:     s.Host,
			username: s.Username,
			password: pw,
			insecure: s.InsecureSkipVerify,
		})
	}
	return out, nil
}
```

- [ ] **Step 6: Tidy, run tests, verify pass**

```bash
GOFLAGS=-mod=mod go mod tidy
go test ./internal/veeam/... -race -v
go build ./...
```
Expected: all parse + source + NewSources tests PASS; `go build ./...` clean. `go.mod` now has `go-resty/resty/v2` + `logrus`.

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "feat: hand-rolled Veeam EM resty client + Source (session auth, httptest oracle)" -m "Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01XksHzPMuvUvvkbPgLWXTN9"
```

---

## Task 3: Thin main.go + consumer Config + config.yaml

Add the binary: cobra → `core.Main`, `Config` embeds `core.Base`, veeam-only `config.yaml`. Default port **9107**.

**Files:**
- Create: `config.go`, `main.go`, `main_test.go`, `config.yaml`

**Interfaces:**
- Consumes core v1.0.0: `core.Base` (`Validate()`; embeds `Collection.Interval` + `OTLP.{Endpoint,Insecure}`, yaml-inline); `core.LoadYAML`; `core.Main`; `core.App{Version, Addr string; Once, Debug, Trace bool; ConfigPath string; Load func() (core.Base, []core.Source, error)}`; `core.Source`.
- Consumes from Task 2: `veeam.NewSources(veeam.VeeamConfig) ([]core.Source, error)`, `veeam.VeeamConfig`.

- [ ] **Step 1: Write the failing test** `main_test.go`

```go
package main

import (
	"os"
	"path/filepath"
	"testing"

	core "github.com/fjacquet/licenses-exporter-core"
)

func TestLoadConfigParsesBaseAndVeeam(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yaml := `
collection:
  interval: 3h
otlp:
  endpoint: "otel:4317"
  insecure: true
veeam:
  enabled: true
  servers:
    - instance: em-a
      host: https://em-a.example.com:9398
      username: svc-ro
      password: shhh
      insecureSkipVerify: true
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	var cfg Config
	if err := core.LoadYAML(path, &cfg); err != nil {
		t.Fatalf("LoadYAML: %v", err)
	}
	if cfg.Collection.Interval.Hours() != 3 {
		t.Errorf("interval = %v, want 3h", cfg.Collection.Interval)
	}
	if cfg.OTLP.Endpoint != "otel:4317" {
		t.Errorf("otlp endpoint = %q, want otel:4317", cfg.OTLP.Endpoint)
	}
	if !cfg.Veeam.Enabled || len(cfg.Veeam.Servers) != 1 || cfg.Veeam.Servers[0].Instance != "em-a" {
		t.Errorf("veeam block not parsed: %+v", cfg.Veeam)
	}
}

func TestLoadReturnsSourcesForEnabledServer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yaml := `
collection:
  interval: 2h
veeam:
  enabled: true
  servers:
    - instance: em-a
      host: https://em-a.example.com:9398
      username: svc-ro
      password: shhh
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
	if len(sources) != 1 || sources[0].Vendor() != "veeam" || sources[0].Instance() != "em-a" {
		t.Fatalf("sources = %+v, want one veeam/em-a", sources)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test . -run TestLoad -v`
Expected: FAIL — `undefined: Config`, `undefined: loadConfig`.

- [ ] **Step 3: Create `config.go`** (use promoted selectors — `cfg.Validate()`, `cfg.Collection`, `cfg.OTLP` — NOT `cfg.Base.X`, which staticcheck QF1008 flags; the value-return `cfg.Base` stays)

```go
package main

import (
	core "github.com/fjacquet/licenses-exporter-core"
	"github.com/fjacquet/veeam_licenses_exporter/internal/veeam"
)

// Config is the exporter's full config: the shared core.Base (collection + otlp)
// inline, plus the vendor-specific veeam block.
type Config struct {
	core.Base `yaml:",inline"`
	Veeam     veeam.VeeamConfig `yaml:"veeam"`
}

// loadConfig parses the file and builds the sources — the single closure body
// core.Main calls at startup and on every reload.
func loadConfig(path string) (core.Base, []core.Source, error) {
	var cfg Config
	if err := core.LoadYAML(path, &cfg); err != nil {
		return core.Base{}, nil, err
	}
	if err := cfg.Validate(); err != nil {
		return core.Base{}, nil, err
	}
	sources, err := veeam.NewSources(cfg.Veeam)
	if err != nil {
		return core.Base{}, nil, err
	}
	return cfg.Base, sources, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test . -run TestLoad -v`
Expected: PASS (both).

- [ ] **Step 5: Create `main.go`** (default addr `:9107`)

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
		Use:   "veeam_licenses_exporter",
		Short: "Veeam (Enterprise Manager) license Prometheus + OTLP exporter",
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
	root.Flags().StringVar(&addr, "web.listen-address", ":9107", "metrics listen address")
	root.Flags().BoolVar(&debug, "debug", false, "debug logging")
	root.Flags().BoolVar(&once, "once", false, "run one collection cycle and exit")
	root.Flags().BoolVar(&trace, "trace", false, "log repo-owned API responses (session token redacted; SDK tracing n/a)")
	if err := root.Execute(); err != nil {
		logrus.WithError(err).Fatal("exporter failed")
	}
}
```

- [ ] **Step 6: Create `config.yaml`**

```yaml
# veeam_licenses_exporter — Veeam Backup Enterprise Manager license exporter.
# Requires Veeam Backup Enterprise Manager (:9398) — standalone VBR has no license REST API.
# Secrets are ${ENV} refs (expanded at load) or passwordFile paths.

collection:
  interval: 2h            # how often to poll Enterprise Manager /api/licensing

otlp:
  endpoint: ""            # empty disables OTLP; e.g. "otel-collector:4317"
  insecure: false

veeam:
  enabled: true
  servers:
    - instance: primary
      host: ${VEEAM_EM_HOST}       # e.g. https://em.example.com:9398
      username: ${VEEAM_USERNAME}
      password: ${VEEAM_PASSWORD}
      # passwordFile: /run/secrets/veeam_password  # alternative to password
      insecureSkipVerify: false
```

- [ ] **Step 7: Build + smoke**

```bash
go build -ldflags="-s -w -X main.version=dev" -o bin/veeam_licenses_exporter .
./bin/veeam_licenses_exporter --help
printf 'collection:\n  interval: 2h\nveeam:\n  enabled: false\n' > /tmp/veeam-smoke.yaml
./bin/veeam_licenses_exporter --once --config /tmp/veeam-smoke.yaml
```
Expected: `--help` lists the five flags with `Use: veeam_licenses_exporter` and `--web.listen-address` default `:9107`. `--once` with `enabled: false` exits 0, no output, no secret in output.

- [ ] **Step 8: Full suite + commit**

```bash
go test ./... -race
git add -A
git commit -m "feat: thin main delegating to core.Main; veeam-only config.yaml (:9107)" -m "Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01XksHzPMuvUvvkbPgLWXTN9"
```
Expected: `go test ./...` PASS (veeam collector + the two main tests).

---

## Task 4: Family scaffolding + docs + ADR-0001 + CHANGELOG

Add the family build/release/deploy scaffolding + docs, adapted from the `vmware_licenses_exporter` template (`vmware`→`veeam`, `9106`→`9107`, govmomi→resty). Records ADR-0001 (consume core + hand-rolled resty EM client). Prominently documents the **Enterprise Manager requirement** and the **v0.1.0 field-verification caveat**.

**Files:**
- Create (from vmware template, renamed): `Makefile`, `.goreleaser.yaml`, `Dockerfile`, `Dockerfile.goreleaser`, `docker-compose.yml`, `docker-compose.ghcr.yml`, `.golangci.yml`, `LICENSE`, `.github/**`, `mkdocs.yml`, `prometheus.yml`, `grafana/**`, `deploy/prometheus/license.rules.yml`
- Create (veeam-specific): `README.md`, `docs/metrics.md`, `docs/dashboards.md`, `docs/deployment/docker.md`, `docs/adr/index.md`, `docs/adr/0001-consume-core-resty-em.md`, `CHANGELOG.md`

- [ ] **Step 1: Copy the build/release scaffolding, renamed**

For each file, read the vmware version and write it into the veeam repo with `vmware_licenses_exporter`→`veeam_licenses_exporter`, `vmware`→`veeam`, `9106`→`9107` substituted:
```
Makefile  .goreleaser.yaml  Dockerfile  Dockerfile.goreleaser
docker-compose.yml  docker-compose.ghcr.yml  .golangci.yml  mkdocs.yml  prometheus.yml
grafana/provisioning/datasources/datasource.yml
grafana/provisioning/dashboards/dashboards.yml
grafana/dashboards/licenses-overview.json
deploy/prometheus/license.rules.yml
.github/dependabot.yml  .github/workflows/{ci,docs,release,security}.yml
```
Source: `git -C ~/Projects/vmware_licenses_exporter show HEAD:<path>`. Also copy `LICENSE` from there. Set `Makefile` `BIN = veeam_licenses_exporter`; keep `release`/`release-snapshot` at `--parallelism 1`; keep target names + `LDFLAGS`. `mkdocs.yml` `extra.version` will be set at release (Task 5).
- **`.golangci.yml`:** add a path-scoped gosec **G402** exclusion for `internal/veeam/client.go` (the operator-opt-in `InsecureSkipVerify` for self-signed EM certs), with a one-line comment — NOT an inline `//nolint`. Keep the `main.go`/`_test.go` exclusions. The four ratified `# nosemgrep` CI-caller lines stay verbatim.

- [ ] **Step 2: `docs/metrics.md`** (Veeam examples, same 7 metric names)

Document the generic `license_` schema with Veeam examples:
```
license_seats_total{vendor="veeam",product="Enterprise Plus",unit="instances",instance="em-a"} 100
license_seats_used{vendor="veeam",product="Enterprise Plus",unit="instances",instance="em-a"} 42
license_expiration_timestamp_seconds{vendor="veeam",product="Enterprise Plus",instance="em-a"} 1.8015264e+09
```
Include the exact seven names (`license_seats_total`, `license_seats_used`, `license_expiration_timestamp_seconds`, `license_up`, `license_collector_last_success_timestamp_seconds`, `license_scrape_duration_seconds`, `license_build_info`); the unlimited→omit-seats_total and perpetual→no-expiration rules; and a note that field mapping is verified against Enterprise Manager v12 (subject to change — v0.1.0).

- [ ] **Step 3: `README.md`, `docs/dashboards.md`, `docs/deployment/docker.md`**

`README.md`: title `# veeam_licenses_exporter`; a Veeam license exporter on `licenses-exporter-core`; **a prominent "Requires Veeam Backup Enterprise Manager (:9398)" note** (standalone VBR has no license REST API); the EM read-only account note (a Veeam EM role that can read licensing); run/compose (binary `veeam_licenses_exporter`, port `9107`, `VEEAM_EM_HOST`/`VEEAM_USERNAME`/`VEEAM_PASSWORD`); a **"v0.1.0 — license field mapping pending verification against a live Enterprise Manager"** caveat; family note + ADR-0001 link. `docs/dashboards.md` + `docs/deployment/docker.md`: adapt from vmware — veeam examples, port `9107`, `VEEAM_*` env vars, EM requirement; no vmware/vCenter prose.

- [ ] **Step 4: ADR-0001 + index**

Create `docs/adr/0001-consume-core-resty-em.md`:
```markdown
# 1. Consume licenses-exporter-core; hand-rolled resty client to Enterprise Manager

Date: 2026-07-02

## Status
Accepted

## Context
This exporter is the Veeam sibling in the licenses_exporter family. The
vendor-neutral engine lives in `github.com/fjacquet/licenses-exporter-core`.
Veeam license data is exposed only via the Veeam Backup Enterprise Manager REST
API (`:9398/api`), not the VBR REST API (`:9419`, which has no license endpoint).
There is no official Veeam Go SDK; the unofficial VeeamHub SDK targets VBR and does
not cover licensing.

## Decision
Depend on `licenses-exporter-core` and build every sample through its constructors.
`main.go` delegates the whole lifecycle to `core.Main`. Read license data from
Enterprise Manager via a hand-rolled `resty/v2` client (session auth → GET
/api/licensing → logout), matching the family's hand-rolled backup exporter
(`nbu_exporter`). No SDK dependency.

## Consequences
- Requires Enterprise Manager to be installed and reachable — documented in the README.
- Schema identity is guaranteed by construction — no local `license_` metric code.
- The EM /api/licensing JSON field mapping is isolated in `internal/veeam/model.go`
  and unverified against a live EM at first release → first tag is v0.1.0 until verified.
- Startup is fatal on an unbuildable-but-valid config (core behaviour); see the core CHANGELOG.
```
Create `docs/adr/index.md` listing ADR-0001.

- [ ] **Step 5: `CHANGELOG.md`**

```markdown
# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project adheres to
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Initial release: a Veeam license exporter reading the Veeam Backup Enterprise Manager
  REST API (`:9398`, session auth → `GET /api/licensing`) via a hand-rolled resty client,
  built on `github.com/fjacquet/licenses-exporter-core`. Emits the shared `license_` schema
  (`vendor="veeam"`, `unit="instances"`). Default metrics port `9107`. Requires Enterprise
  Manager. See ADR-0001. Released as **v0.1.0**: the EM `/api/licensing` field mapping is
  isolated (`internal/veeam/model.go`) and pending verification against a live Enterprise
  Manager; the parser is tolerant (absent-not-zero) until then.
```

- [ ] **Step 6: Run the full gate**

```bash
cd ~/Projects/veeam_licenses_exporter
make ci
make release-snapshot
uvx --with mkdocs-material --with pymdown-extensions mkdocs build --strict
uvx semgrep scan --config auto --skip-unknown-extensions .
```
Expected: `make ci` green (gofmt, vet, golangci-lint 0 issues incl. the G402 path exclusion, `-race` tests pass, govulncheck clean, binary builds as `bin/veeam_licenses_exporter`). `make release-snapshot` artifacts named `veeam_licenses_exporter`. mkdocs `--strict` no warnings. semgrep 0 findings.

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "chore: family scaffolding + docs + ADR-0001 (veeam_licenses_exporter, :9107)" -m "Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01XksHzPMuvUvvkbPgLWXTN9"
```

---

## Task 5: Publish v0.1.0 (ops — after the whole-repo review)

Not a TDD task. Confirm visibility + version with the user before pushing anything outward-facing.

- [ ] **Step 1: Set `mkdocs.yml` `extra.version` to `v0.1.0`; final `make ci`.**

- [ ] **Step 2: Publish** (public repo, first tag `v0.1.0` per the field-verification caveat):

```bash
cd ~/Projects/veeam_licenses_exporter
gh repo create fjacquet/veeam_licenses_exporter --public --source=. --remote=origin \
  --description "Veeam license exporter (Prometheus + OTLP) via Enterprise Manager — licenses_exporter family, built on licenses-exporter-core"
git push -u origin main
git tag -a v0.1.0 -m "veeam_licenses_exporter v0.1.0 — EM licensing; field mapping pending live verification"
git push origin v0.1.0
```
Verify `gh release view` shows GoReleaser assets named `veeam_licenses_exporter`.

- [ ] **Step 3: Record the post-v0.1.0 follow-up** — verify the EM `/api/licensing` field names against a live Enterprise Manager; correct `internal/veeam/model.go` if needed; promote to v1.0.0.

---

## Self-Review

**1. Spec coverage:**
- Consume core v1.0.0, EM REST target, hand-rolled resty → Tasks 1-2. ✅
- Session auth stateless per cycle, logout warn, retry-excludes-4xx → Task 2 client.go/source.go. ✅
- Tolerant parser, absent-not-zero, unlimited/perpetual edge cases → Task 1 parse.go + parse_test. ✅
- JSON model isolated (model.go) + v0.1.0 caveat → Task 1 model.go + Task 4/5 docs. ✅
- Thin main + Config embedding core.Base, port 9107 → Task 3. ✅
- Secrets via ResolveSecret, token never logged, --trace redaction → Task 2 + Task 3 flag help + README. ✅
- httptest oracle → Task 2 source_test. ✅
- ADR-0001 + metrics.md + CHANGELOG + EM-requirement docs → Task 4. ✅
- Publish v0.1.0 public → Task 5. ✅
- Family CI/GoReleaser/port 9107 → Task 4. ✅

**2. Placeholder scan:** No "TBD"/"handle errors"/"similar to". Complete code in every code step. The G402 note is a concrete config-scoped instruction (not an inline suppression); the field-verification caveat is an explicit, documented v0.1.0 decision, not a code gap.

**3. Type consistency:** `VeeamConfig`/`ServerConfig` field names consistent between Task 1 (definition) and Task 3 (`loadConfig`, test YAML). `parseLicense(raw []byte, instance string) ([]core.Sample, error)` consistent between Task 1 (def) and Task 2 (source.Collect). `newEMClient`/`login`/`licensing`/`logout`, `source{instance,host,username,password,insecure}`, `NewSources(VeeamConfig) ([]core.Source, error)` consistent across Tasks 2-3. core surface (`SeatSample`, `ExpirationSample`, `MetricSeatsTotal/Used/Expiration`, `ResolveSecret`, `Base`, `LoadYAML`, `Main`, `App`, `Source`) matches published v1.0.0. `vendor="veeam"`, `unit="instances"` consistent.
