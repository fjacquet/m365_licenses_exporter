# 0007. Token/credential auth with a retry policy that excludes 4xx

- **Status:** accepted
- **Date:** 2026-07-02
- **Deciders:** Fred Jacquet

## Context and problem statement

Both collectors authenticate against a remote control plane that can fail transiently
(network blips, transport errors, 5xx) or permanently (bad credentials, insufficient
permissions, 4xx). Sibling exporters that hand-roll their HTTP client (`ppdd_exporter`,
`pstore_exporter`) implement their own retry predicate to guarantee "retry transient only,
never retry 4xx". `licenses_exporter` uses **official SDKs for both vendors**
([ADR-0003](0003-client-choice-govmomi-sdk-and-msgraph-sdk.md)), which are non-injectable —
there is no repo-owned HTTP round-tripper to attach a custom retry predicate to. The design
still has to answer: what actually happens on an auth failure for each vendor, and is a bad
credential ever retried in a way that could hammer an IdP or lock an account?

## Considered options

- **Hand-roll a resty-based retry layer around each SDK's transport** — matches the sibling
  pattern exactly, but both SDKs are non-injectable, so this would mean bypassing the SDKs'
  own HTTP stacks entirely (defeating the reason they were chosen in ADR-0003).
- **Rely on each SDK's own transient-only retry behavior, and keep the exporter's own
  collection loop from adding retries on top** — accept the SDK boundary, verify what each
  SDK actually does, and make sure the exporter layer never retries an auth failure itself.

## Decision outcome

Chosen option: **rely on SDK-native retry, with a single-attempt-per-cycle auth flow at the
exporter layer.**

- **VMware.** Each 2h collection cycle is stateless (login → single `LicenseManager` query →
  logout+close; see [ADR-0002](0002-prometheus-snapshot-model.md)). `govmomi.NewClient`
  performs exactly one login attempt per cycle; if it fails for any reason — including a bad
  credential — `Collect` returns an error, the cycle degrades to `license_up{vendor,instance}=0`
  (`internal/license/collector.go`), and the *next* attempt is the next scheduled cycle (or
  sooner via a config reload). The exporter never retries a failed vCenter login within a
  cycle, so a persistently bad credential never turns into a login-attempt storm against
  vCenter.
- **Microsoft 365.** Auth is `azidentity.NewClientSecretCredential` (client-credentials flow),
  consumed by `msgraphsdk.NewGraphServiceClientWithCredentials`. Both the Azure Identity SDK's
  token-acquisition pipeline and the Graph SDK's underlying Kiota HTTP client apply their own
  transient-only retry policies (5xx / 429 with backoff) and do **not** retry hard 4xx
  failures such as an invalid client secret or `403 Forbidden` from a missing
  `Organization.Read.All` grant — those surface as an error from `listSkus`/`Collect` on the
  first attempt, again degrading that cycle's `license_up` to `0` rather than looping.
- **The exporter adds no retry layer of its own on top of either SDK.** The collection loop
  (`errgroup`-based fan-out in `internal/license/collector.go`) treats any `Source.Collect`
  error as a one-shot failure for that cycle; retry cadence for a genuinely down or
  misconfigured target is the next `collection.interval`, not an in-cycle loop.
- **`--trace` never enables SDK debug modes.** Both SDKs' verbose/debug logging includes
  bearer tokens and, for `govmomi`, the session cookie — turning it on would leak credentials
  into logs. `--trace` is scoped to repo-owned transports only; because both v1 collectors use
  non-injectable SDK HTTP stacks, `--trace` currently has **no wire-level visibility** into
  either vendor's traffic — `main.go` logs an explicit warning to that effect rather than
  silently doing nothing. A future hand-rolled client (or an SDK middleware hook, if one
  becomes available) would need to close that gap deliberately, never by flipping on SDK
  debug output.

### Consequences

- Good — no exporter-side retry storm or account-lockout risk against either vCenter or
  Entra ID; a bad credential fails fast, once, per cycle.
- Good — no bearer token or session cookie is ever written to exporter logs, even under
  `--trace`.
- Bad — the exporter cannot independently guarantee "5xx retries, 4xx doesn't" the way a
  hand-rolled client can assert in its own test suite; it depends on each SDK continuing to
  honor that behavior. A regression in either SDK's retry policy would only surface as
  observed behavior (excess request volume, or failure to recover from a transient blip),
  not as a local unit-test failure.
- Bad — `--trace` cannot show the actual M365/VMware request/response bodies for live payload
  validation the way it could for a hand-rolled client; `--once --debug`'s sample dump
  (see `docs/metrics.md`) is the primary live-validation tool instead.
- Neutral — this deviates from the sibling exporters' hand-rolled resty retry predicate
  pattern; that deviation is a direct, accepted consequence of ADR-0003's SDK choice, not an
  independent decision.

## Related

- [0002. Prometheus snapshot model](0002-prometheus-snapshot-model.md)
- [0003. Client choice: govmomi SDK and msgraph-sdk-go](0003-client-choice-govmomi-sdk-and-msgraph-sdk.md)
