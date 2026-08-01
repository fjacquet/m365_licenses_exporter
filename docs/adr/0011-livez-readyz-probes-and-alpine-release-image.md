# 11. `/livez` + `/readyz` probes and an Alpine release image

Date: 2026-08-01

## Status
Accepted

## Context
Two family-wide standards completed on 2026-08-01 — the always-200 `/livez` +
`/readyz` probe pattern, and the Alpine container-image standard — both skipped
this repo, because the `exporter-standards` skill's family table listed eight
repos and this one was not among them.

Concretely, two defects followed. First, `/health` (served by
`licenses-exporter-core`) returned **503 `starting`** until the first collection
cycle completed. With a default `collection.interval` and a slow Graph tenant that
window is long enough for a Kubernetes `livenessProbe` to restart a perfectly
healthy process, and long enough for a Docker `HEALTHCHECK` to report the
container unhealthy throughout start-up. Second, the published image was
`gcr.io/distroless/static:nonroot`, which has no shell and no `wget` — so the
image *could not carry a `HEALTHCHECK` at all*, while the local `./Dockerfile`
was already Alpine at uid 10001. The two build paths disagreed about the runtime.

All three licenses exporters share their HTTP wiring through
`licenses-exporter-core`: it owns the only `http.ServeMux` and the only `/health`
handler, and no consumer registers a route. So the probe half is one library
change plus three dependency bumps.

## Decision
Consume `licenses-exporter-core` **v1.1.0**, which serves `/livez` and `/readyz`
from a handler that reads no state and makes `/health` answer 200
unconditionally, keeping `starting`/`ok` as body content.

Convert `Dockerfile.goreleaser` from `gcr.io/distroless/static:nonroot` to
`alpine:latest`, running as the named user `licenses` at uid **10001** — matching
the local `./Dockerfile` and the rest of the exporter family. Add a `HEALTHCHECK`
against `http://127.0.0.1:9105/livez` to both Dockerfiles and a matching
`healthcheck:` to both compose files.

`127.0.0.1` and never `localhost`: busybox `wget` resolves `localhost` via `::1`
first and this exporter binds IPv4 only, so a `localhost` check fails at runtime
while passing both `hadolint` and `docker compose config`.

Probes never point at `/metrics`: rendering the full exposition per probe tick is
needless load and can block behind a slow collection cycle.

## Consequences
- **Breaking:** the published container's uid moves `65532` → `10001`. Anyone
  pinning it — a `securityContext.runAsUser`, ownership on a mounted secret or
  log volume — must update. This repo ships no Helm chart, so there is no chart
  default to change.
- The release image gains a shell and busybox. That is a deliberate trade: a
  working `HEALTHCHECK` and one base image across fifteen repos, bought with a
  larger attack surface than distroless.
- `alpine:latest` is unpinned, family-wide. It is the one build input whose
  contents can change between two builds of the same commit, which cuts against
  ADR-0001's supply-chain posture. Uniformity was chosen over reproducibility;
  revisiting it is a fifteen-repo decision, not a per-repo one.
- Anything asserting a 503 from `/health` — an alert rule, a smoke test, a
  blackbox-exporter check — must move to reading the body, or to `/readyz`.
- Engine behaviour arrives by a core version bump, not a local edit (ADR-0010).
