# 0008. Config hot reload: cancelable context + last-good-snapshot continuity

- **Status:** accepted
- **Date:** 2026-07-02
- **Deciders:** Fred Jacquet

## Context and problem statement

`collection.interval` defaults to 2h, so a config change (a new tenant, a rotated secret, an
added vCenter) applied by simply "wait for the next cycle" could take up to two hours to take
effect, and a naive reload that tears down the running server while rebuilding could blank
`/metrics` mid-reload. The exporter needs a reload path that (a) applies a config change
promptly, (b) never serves a partial or empty `/metrics` response during the swap, and (c)
never silently accepts a broken config and crashes the running process.

## Considered options

- **Log-only reload** — detect a SIGHUP/file change and log it; require a manual restart to
  actually apply anything. Honest but not "hot" in any real sense.
- **Cancelable-context rebuild-and-swap** — cancel the active collection cycle's context
  immediately, validate the new config *before* discarding the old one, and only swap in the
  new collection loop once it has actually produced a config that loads.

## Decision outcome

Chosen option: **cancelable-context rebuild-and-swap** (`serveWithReload` in `main.go` drives
`app.Server.ReloadLoop` over the process-lifetime stack built once by `app.NewServer`):

0. The **serving stack is built exactly once** at startup by `app.NewServer`: the shared
   `SnapshotStore`, the Prometheus registry+collector, the OTLP push exporter, the `/health`
   handler, and a single `http.Server` whose listener is bound up front via `net.Listen`. A
   bind failure is returned synchronously (startup-fatal, acceptable); a *runtime* serve error
   is only logged, never fatal. Reload **never** rebuilds the server or rebinds the socket, so
   it can never fail with "address already in use" and can never kill the process.
1. Each background collection loop (`Server.RunCollection`) executes under a `context.Context`
   created fresh per loop iteration via `context.WithCancel`. `RunCollection` runs exactly one
   `CollectOnce` (marking `/health` ready after that first cycle) and then hands off to a
   ticker-only loop (`Collector.RunTicker`) — never a double back-to-back initial collect.
2. On `SIGHUP` or an `fsnotify` write/create event on the config file, the **candidate**
   config is loaded and validated (`config.Load`) *before* anything about the running loop is
   touched. If the candidate fails to load or fails validation, it is logged and discarded —
   the wait loop simply continues waiting on the *same, still-running* server and config; the
   process never crashes and never tears down a working loop for a broken candidate.
3. Only once a valid new config is obtained does the outer loop cancel the current cycle's
   context (aborting any in-flight VMware/M365 SDK request immediately) and spawn a brand-new
   `Server.RunCollection` goroutine against the new config on the **same** server and store,
   resetting the `collection.interval` timer from zero.
4. The `SnapshotStore` is a single, shared instance across every loop iteration — it is never
   replaced or recreated on reload. `/metrics` and `/health` keep serving the **last-good
   snapshot** produced by the outgoing loop until the new loop's first `CollectOnce` swaps in
   a fresh one; `/metrics` never goes blank across a reload, and `/health` never flips back to
   `503` (it is set ready once and the store always holds a valid snapshot thereafter).
5. `SIGINT`/`SIGTERM` cancel the active context and exit `serveWithReload` for a clean
   shutdown; they are handled on the same signal channel as `SIGHUP` but take the shutdown
   branch instead of the reload branch.

### Consequences

- Good — a config change is applied within one reload cycle (seconds), not up to a full
  `collection.interval` later.
- Good — `/metrics` is never blank or partial during a reload; the last-good snapshot serves
  continuously through the swap.
- Good — an invalid candidate config (typo, bad YAML, missing required field) is rejected
  without disturbing the currently-running, previously-validated configuration or crashing
  the process.
- Bad — in-flight VMware/M365 requests are aborted (not drained) on reload; a reload that
  lands mid-cycle discards that cycle's partial progress rather than letting it finish. This
  is accepted because collection cycles are cheap to restart and pure (idempotent,
  stateless — see [ADR-0002](0002-prometheus-snapshot-model.md)).
- Neutral — reload validation and rebuild happen serially on a single goroutine driven by one
  signal/file-watch channel; concurrent SIGHUPs do not race against each other.

## Related

- [0002. Prometheus snapshot model](0002-prometheus-snapshot-model.md)
