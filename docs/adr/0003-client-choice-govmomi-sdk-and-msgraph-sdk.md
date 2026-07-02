# 0003. Client choice: `govmomi` SDK and `msgraph-sdk-go` (roadmap-justified exception)

- **Status:** accepted
- **Date:** 2026-07-02
- **Deciders:** Fred Jacquet

## Context and problem statement

The exporter family's default rule is: *use the official vendor Go SDK if it is both
**available** and **useful**; otherwise hand-roll a thin `resty/v2` client* (see, e.g.,
`pstore_exporter`'s use of `gopowerstore`, or `obs_exporter`'s hand-rolled client where no
usable SDK exists). `licenses_exporter` has two vendors to evaluate independently against
that rule: VMware vSphere and Microsoft 365 (Graph).

## Considered options

**VMware:**
- Hand-roll a SOAP/REST client against the vSphere API.
- Use `github.com/vmware/govmomi`, VMware's official Go SDK.

**Microsoft 365:**
- Hand-roll a `resty` client + `azidentity` calling `GET /v1.0/subscribedSkus` directly (one
  endpoint, minimal surface).
- Use `github.com/microsoftgraph/msgraph-sdk-go`, the official (generated) Graph SDK.

## Decision outcome

### VMware — `govmomi` (SDK): clean fit, use it

`govmomi` is a clean SDK-yes: session-auth login/logout is a first-class flow, and
`LicenseManager.List` (`internal/vmware/source.go`) resolves the entire license inventory in a
**single property-collector fetch** — no N+1 pagination — returning a fully-typed
`[]types.LicenseManagerLicenseInfo` (`Total`, `Used`, `CostUnit`, `Name`,
`Properties["expirationDate"]`). There is no meaningful hand-rolled alternative that would be
simpler or more correct; the SDK is used as-is.

### Microsoft 365 — `msgraph-sdk-go` (SDK): a roadmap-justified exception

The strict rule would favor hand-rolling: v1 only calls one Graph endpoint
(`subscribedSkus`), and the full generated Graph SDK is a genuinely heavy dependency tree for
that single call — the family's usual "irrelevant dependency tree" red flag. `msgraph-sdk-go`
is adopted anyway, as an explicit, **forward-looking exception**:

- Design spec §10 already commits to a **phase-2 Entra ID identity spec** (users,
  `signInActivity`, MFA status) that will lean heavily on Graph across multiple endpoints and
  will need the SDK's pagination (`msgraphcore.NewPageIterator`), OData query building, and
  typed models regardless. Paying the dependency-tree cost once, now, amortizes across that
  future identity domain instead of being paid twice (once hand-rolled now, again when the
  SDK becomes unavoidable later).
- Auth is `azidentity.NewClientSecretCredential` (client-credentials flow against
  `https://graph.microsoft.com/.default`), which the SDK consumes directly via
  `msgraphsdk.NewGraphServiceClientWithCredentials` — no separate token-management code is
  needed even though the deviation is toward the heavier SDK.
- Pagination is handled honestly rather than assumed away: `graphSkuLister.listSkus`
  (`internal/m365/graph.go`) always constructs a `PageIterator` and follows
  `@odata.nextLink`, even though `subscribedSkus` rarely spans more than one page — a large
  tenant is never silently truncated.
- The required Graph **application permission** is `Organization.Read.All` (or
  `Directory.Read.All`), granted to the Entra ID app registration ahead of first run — see
  `docs/deployment/docker.md`.

### Consequences

- Good — VMware gets a well-typed, single-fetch, low-maintenance license query for free from
  the vendor SDK.
- Good — the M365 collector is pagination-safe from day one and the phase-2 Entra ID spec
  inherits a working Graph client pattern instead of starting from zero.
- Bad — `msgraph-sdk-go` is a materially heavier dependency for what v1 uses as a single
  endpoint; this is accepted explicitly as amortized against the roadmap, not free.
- Bad — both SDKs are **non-injectable** transports: neither can be wrapped with a repo-owned
  `OnAfterResponse` hook the way a hand-rolled `resty` client can. `--trace` therefore cannot
  observe raw M365/VMware wire traffic (see [ADR-0007](0007-token-auth-retry-policy.md)).
- Neutral — if a phase-2 Entra ID spec is abandoned or descoped, this ADR's justification for
  the M365 SDK choice should be revisited rather than assumed to still hold.

## Related

- [0007. Token/credential auth with a retry policy that excludes 4xx](0007-token-auth-retry-policy.md)
