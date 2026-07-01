# CLAUDE.md

Guidance for working in `licenses_exporter`. Design spec:
`docs/superpowers/specs/2026-07-01-licenses-exporter-design.md`.

## Commands
- `make cli` — build `bin/licenses_exporter`.
- `make test` / `make test-race` — tests.
- `make tools` — install pinned dev/CI tooling (golangci-lint, cyclonedx-gomod, govulncheck).
- `make ci` — gofmt check + vet + lint + race tests + govulncheck + build (the CI gate).
- `make release-snapshot` — local GoReleaser dry-run (binaries + archives + SBOM + checksums).
- Run: `./bin/licenses_exporter --config config.yaml [--once] [--debug] [--trace]`. Secrets are
  `${ENV}` refs in `config.yaml` (or `passwordFile`/`clientSecretFile`). `--once --debug` dumps
  every collected sample (sorted, exposition style); `--trace` logs every **repo-owned** API
  response body for live payload validation (never SDK debug modes — they leak the token).
- Demo stack: `docker compose up` (exporter + Prometheus + Grafana, auto-provisioned; :9105).
- Docs: `uvx --with mkdocs-material --with pymdown-extensions mkdocs build --strict`.

## Architecture
Unified enterprise **license** exporter for the Prometheus/Grafana stack. A background
**collection loop** (`internal/license/collector.go`) polls every configured target on
`collection.interval` (default 2h) and publishes an immutable **snapshot** to a `SnapshotStore`
(RWMutex pointer-swap). Both export paths read the latest snapshot, decoupling vendor-API load
from scrape count: `/metrics` (`prometheus.go`, an *unchecked* collector) and the OTLP push
(`otlp.go`, observable gauges). `main.go` wires the server, loop, hot reload, and `/health` —
HTTP is served **before** the first collection cycle.

Collection is **modular by vendor target**: each configured tenant/vCenter is one `Source`
(`source.go`) — `Vendor()`, `Instance()`, `Collect(ctx) → []Sample`. Vendor packages build
their `[]Source` from config: `internal/m365` (Microsoft Graph via `msgraph-sdk-go`) and
`internal/vmware` (vSphere `LicenseManager` via `govmomi`). A source failure degrades to
`license_up{vendor,instance}=0`, never crashing the cycle.

## Conventions (load-bearing)
- **Generic schema, vendor labels.** One `license_` prefix (novel vs. the family's per-vendor
  prefix — see ADR); vendors are distinguished by `vendor,product,unit,instance` labels, built
  from the shared builders in `internal/license/metrics.go`.
- **Raw facts, absent-never-zero.** Expose `license_seats_total`, `license_seats_used`, and
  `license_expiration_timestamp_seconds` (**omitted** when perpetual — no `9999` sentinel).
  No exporter-computed `days_to_expiration` or `compliance_status`; derive those in PromQL /
  alert rules. An unparseable value yields an **absent** sample, never a fake `0`. Two
  concrete cases: at **cold start** only `license_build_info` is emitted — no `license_up`
  or target series until each source's first collect resolves; a **VMware unlimited** key
  (`Total <= 0`) omits `license_seats_total` and emits only `license_seats_used`.
- **Label-key consistency.** A metric name carries one label-key set across all series (all
  vendors); a label-parity test guards this.
- **Auth.** VMware = govmomi session login, **stateless per 2h cycle** (login → query →
  logout+close; no persisted cookie); M365 = `azidentity` client-credentials (via the SDK),
  Graph app permission `Organization.Read.All`. Retry **excludes 4xx** (never retry auth
  failures). Both SDKs are non-injectable, so `--trace` wraps only repo-owned transports —
  never enable SDK debug (leaks bearer / `Set-Cookie`).
- **Reload is cancelable.** Each collection cycle runs under a cancelable context; SIGHUP
  cancels in-flight requests, re-validates config, respawns the loop, and keeps serving the
  last-good snapshot until the new one is ready (never blanks `/metrics`).
- **config.yaml is the way.** Collector toggling is `enabled:` per collector, not an env var;
  `${ENV}` refs expand in host/user/secret; `.env` is a convenience, never the source of truth.
- **Always update docs (`docs/metrics.md`) + `CHANGELOG.md`** in the same change as a feature.

## Adding a vendor collector
Create `internal/<vendor>/` with its config struct, a `NewSources(cfg) ([]Source, error)`
constructor, the `Source` implementation(s) (endpoint/SDK call + tolerant `parse → []Sample`
stamping `vendor,product,unit,instance`), and a test (mock transport / SDK interface, or
`vcsim` for VMware). Wire the vendor into the config schema + registry, add an ADR for its
client choice, document metrics in `docs/metrics.md`, and add a `CHANGELOG.md` entry. Assert
via **both** the Prometheus registry gather and an OTLP `ManualReader`. Identity/asset metrics
(AD, Entra) are a *different* metric family — give them their own prefix + schema ADR, not `license_`.

---

<!-- rtk-instructions v2 -->
# RTK (Rust Token Killer) - Token-Optimized Commands

## Golden Rule

**Always prefix commands with `rtk`**. If RTK has a dedicated filter, it uses it. If not, it passes through unchanged. This means RTK is always safe to use.

**Important**: Even in command chains with `&&`, use `rtk`:
```bash
# ❌ Wrong
git add . && git commit -m "msg" && git push

# ✅ Correct
rtk git add . && rtk git commit -m "msg" && rtk git push
```

## RTK Commands by Workflow

### Build & Compile (80-90% savings)
```bash
rtk cargo build         # Cargo build output
rtk cargo check         # Cargo check output
rtk cargo clippy        # Clippy warnings grouped by file (80%)
rtk tsc                 # TypeScript errors grouped by file/code (83%)
rtk lint                # ESLint/Biome violations grouped (84%)
rtk prettier --check    # Files needing format only (70%)
rtk next build          # Next.js build with route metrics (87%)
```

### Test (60-99% savings)
```bash
rtk cargo test          # Cargo test failures only (90%)
rtk go test             # Go test failures only (90%)
rtk jest                # Jest failures only (99.5%)
rtk vitest              # Vitest failures only (99.5%)
rtk playwright test     # Playwright failures only (94%)
rtk pytest              # Python test failures only (90%)
rtk rake test           # Ruby test failures only (90%)
rtk rspec               # RSpec test failures only (60%)
rtk test <cmd>          # Generic test wrapper - failures only
```

### Git (59-80% savings)
```bash
rtk git status          # Compact status
rtk git log             # Compact log (works with all git flags)
rtk git diff            # Compact diff (80%)
rtk git show            # Compact show (80%)
rtk git add             # Ultra-compact confirmations (59%)
rtk git commit          # Ultra-compact confirmations (59%)
rtk git push            # Ultra-compact confirmations
rtk git pull            # Ultra-compact confirmations
rtk git branch          # Compact branch list
rtk git fetch           # Compact fetch
rtk git stash           # Compact stash
rtk git worktree        # Compact worktree
```

Note: Git passthrough works for ALL subcommands, even those not explicitly listed.

### GitHub (26-87% savings)
```bash
rtk gh pr view <num>    # Compact PR view (87%)
rtk gh pr checks        # Compact PR checks (79%)
rtk gh run list         # Compact workflow runs (82%)
rtk gh issue list       # Compact issue list (80%)
rtk gh api              # Compact API responses (26%)
```

### JavaScript/TypeScript Tooling (70-90% savings)
```bash
rtk pnpm list           # Compact dependency tree (70%)
rtk pnpm outdated       # Compact outdated packages (80%)
rtk pnpm install        # Compact install output (90%)
rtk npm run <script>    # Compact npm script output
rtk npx <cmd>           # Compact npx command output
rtk prisma              # Prisma without ASCII art (88%)
```

### Files & Search (60-75% savings)
```bash
rtk ls <path>           # Tree format, compact (65%)
rtk read <file>         # Code reading with filtering (60%)
rtk grep <pattern>      # Search grouped by file (75%). Format flags (-c, -l, -L, -o, -Z) run raw.
rtk find <pattern>      # Find grouped by directory (70%)
```

### Analysis & Debug (70-90% savings)
```bash
rtk err <cmd>           # Filter errors only from any command
rtk log <file>          # Deduplicated logs with counts
rtk json <file>         # JSON structure without values
rtk deps                # Dependency overview
rtk env                 # Environment variables compact
rtk summary <cmd>       # Smart summary of command output
rtk diff                # Ultra-compact diffs
```

### Infrastructure (85% savings)
```bash
rtk docker ps           # Compact container list
rtk docker images       # Compact image list
rtk docker logs <c>     # Deduplicated logs
rtk kubectl get         # Compact resource list
rtk kubectl logs        # Deduplicated pod logs
```

### Network (65-70% savings)
```bash
rtk curl <url>          # Compact HTTP responses (70%)
rtk wget <url>          # Compact download output (65%)
```

### Meta Commands
```bash
rtk gain                # View token savings statistics
rtk gain --history      # View command history with savings
rtk discover            # Analyze Claude Code sessions for missed RTK usage
rtk proxy <cmd>         # Run command without filtering (for debugging)
rtk init                # Add RTK instructions to CLAUDE.md
rtk init --global       # Add RTK to ~/.claude/CLAUDE.md
```

## Token Savings Overview

| Category | Commands | Typical Savings |
|----------|----------|-----------------|
| Tests | vitest, playwright, cargo test | 90-99% |
| Build | next, tsc, lint, prettier | 70-87% |
| Git | status, log, diff, add, commit | 59-80% |
| GitHub | gh pr, gh run, gh issue | 26-87% |
| Package Managers | pnpm, npm, npx | 70-90% |
| Files | ls, read, grep, find | 60-75% |
| Infrastructure | docker, kubectl | 85% |
| Network | curl, wget | 65-70% |

Overall average: **60-90% token reduction** on common development operations.
<!-- /rtk-instructions -->
