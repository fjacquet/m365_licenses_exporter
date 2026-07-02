# 0001. Supply-chain / release hardening

- **Status:** accepted
- **Date:** 2026-07-02
- **Deciders:** Fred Jacquet

## Context and problem statement

A new exporter's CI/CD and release pipeline is either hand-rolled per repo (drift-prone,
re-reviewed every time) or it reuses a hardened, shared implementation. `licenses_exporter`
joins an existing family of Go exporters (`pflex_exporter`, `ppdd_exporter`, `pstore_exporter`,
…) that already converged on SHA-pinned, centrally-maintained CI and a GoReleaser-based
release. The question for a brand-new repo is whether to hand-roll its own workflows/release
shell or adopt the family's hardened pipeline from day one.

## Considered options

- **Hand-roll workflows + a shell-scripted release loop** — full control, but re-introduces
  mutable Action tags and a bespoke cross-compile/checksum/SBOM path that every sibling repo
  has already moved away from.
- **Consume `fjacquet/ci` as thin reusable-workflow callers + GoReleaser** — reuse the
  hardened, centrally-maintained pipeline; the repo owns only its release *inputs*
  (`.goreleaser.yaml`, `Dockerfile`, `Dockerfile.goreleaser`, `dependabot.yml`), not the
  workflow internals.

## Decision outcome

Chosen option: **consume `fjacquet/ci`'s reusable workflows and GoReleaser**, exactly as the
sibling exporters do:

- `.github/workflows/ci.yml`, `security.yml`, `release.yml`, `docs.yml` are four thin caller
  stubs that each invoke one `fjacquet/ci/.github/workflows/<name>.yml@v1` reusable workflow
  (`go-ci`, `go-security`, `go-release`, `docs-publish`). SHA-pinning and hardening for the
  third-party Actions those reusable workflows call live in `fjacquet/ci`, not here — this
  repo never re-inlines or SHA-pins them itself.
- `release.yml` triggers on `v*` tags and forwards `HOMEBREW_TAP_GITHUB_TOKEN` as a secret;
  it requests `contents: write`, `packages: write`, `id-token: write` for the release job.
- `.goreleaser.yaml` (schema `version: 2`) owns cross-compilation (`linux,darwin ×
  amd64,arm64`, `CGO_ENABLED=0`, `-trimpath`, reproducible `mod_timestamp`), `tar.gz`
  archives (bundling `LICENSE`, `README.md`, `config.yaml`) plus raw binaries for scripted
  downloads, `checksums.txt` (sha256), and a CycloneDX SBOM via
  `go run github.com/CycloneDX/cyclonedx-gomod` — the same command and pin
  (`v1.10.0`) as `make sbom`, so the release SBOM matches the CI artifact byte-for-byte.
- A Homebrew **cask** (`homebrew_casks`, macOS-only — the `brews` formula stanza is
  deprecated) publishes to a separate `fjacquet/homebrew-tap` repo, gated on
  `HOMEBREW_TAP_GITHUB_TOKEN` (a cross-repo PAT; the default `GITHUB_TOKEN` cannot push to
  another repo) and self-skipping (`skip_upload`) when that secret is absent — so a release
  never breaks before the tap exists.
- `Dockerfile` is a non-root, multi-stage build: `golang:1.26.4` builder → `alpine:latest`
  runtime, running as an unprivileged `licenses` user (uid 10001), with the CA bundle copied
  from the builder stage (not `apk add ca-certificates`, which fails behind a TLS-intercepting
  proxy before any CA bundle exists to validate it).
- `.github/dependabot.yml` covers **`gomod` and `docker` only** — GitHub Actions are consumed
  as reusable-workflow callers, not local pinned actions, so there is no `github-actions`
  ecosystem entry to keep current here (that responsibility lives in `fjacquet/ci`).
- `make release-snapshot` runs the full GoReleaser pipeline locally (build + archive + SBOM +
  checksums) without publishing, for a pre-tag dry run.

### Consequences

- Good — the release pipeline, its SHA-pinning, and its hardening are maintained once in
  `fjacquet/ci` and inherited by every family exporter, including this one, without local
  workflow maintenance.
- Good — SBOM parity between `make sbom` (CI) and the GoReleaser release artifact avoids two
  divergent SBOM-generation code paths.
- Good — the container image runs as a non-root user with a minimal Alpine base and a
  CA bundle sourced without a network fetch at image-build time.
- Bad — the Homebrew cask needs a one-time setup (an empty `fjacquet/homebrew-tap` repo and a
  `HOMEBREW_TAP_GITHUB_TOKEN` repo secret) before it actually publishes; until then it
  self-skips silently.
- Neutral — release-asset format is `tar.gz` archives (plus raw binaries as a second archive
  id) rather than bare binaries only; consumers scripting direct downloads must pick the
  right archive id.

## Related

- [0002. Prometheus snapshot model](0002-prometheus-snapshot-model.md)
