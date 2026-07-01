# licenses_exporter — Design Review & Decisions Addendum (v1)

- **Date:** 2026-07-01
- **Status:** Approved for planning
- **Reference Spec:** `docs/superpowers/specs/2026-07-01-licenses-exporter-design.md`
- **Reviewer:** Gemini CLI

## 1. Executive Summary

This document serves as the formal review, critique, and decision-addendum to the original `licenses_exporter` Design Spec. The core design is highly robust, adhering to standard Prometheus exporter best practices (decoupling backend API load from scrapers, graceful degradation, and metric-schema invariants). 

To ensure complete robustness, five key operational areas (cold starts, hot-reloads, stateless sessions, unlimited licensing, pagination, and OTLP timestamping) were reviewed and resolved.

---

## 2. Resolved Design Decisions

### 2.1. VMware (vSphere) Connection Management
* **Decision:** **Stateless Connection Lifecycle**
* **Mechanics:** The VMware collector (`Source` implementation) will be fully stateless. 
  1. For each 2-hour collection run, it will establish a fresh connection and log in to the vCenter API using `govmomi`.
  2. It will perform the single PropertyCollector query to retrieve license information from the `LicenseManager`.
  3. It will immediately call `Logout()` and close the connection.
* **Rationale:** This completely avoids session timeout, token expiration, and cookie-maintenance bugs that arise when keeping a connection open for long idle intervals (2 hours).

### 2.2. Unlimited & Infinite Capacity Licenses (vSphere / M365)
* **Decision:** **Omit total capacity metrics, emit used metrics only**
* **Mechanics:** When a license has infinite/unlimited capacity (e.g., evaluation, unlimited academic, or certain enterprise agreements):
  1. The exporter will **completely omit** the `license_seats_total` metric for that specific product series.
  2. The exporter will continue to emit the `license_seats_used` series.
* **Rationale:** This respects the **"absent, never zero/sentinel"** principle. Emitting arbitrary values (like `-1`, `0`, or `math.MaxInt32`) would corrupt PromQL calculations and downstream dashboard displays. Downstream users can detect unlimited licenses in PromQL using `absent(license_seats_total)`.

### 2.3. Startup Cold-Start Behavior
* **Decision:** **Omit target metrics during initial window**
* **Mechanics:** Since the exporter serves HTTP and `/health` immediately at startup (before the first collection completes):
  1. Scraping `/metrics` during this startup window will return **only** standard exporter build and system metrics (e.g., `license_build_info`, `promhttp_*`).
  2. All target-specific license metrics (e.g., `license_seats_...` and `license_up`) will be completely omitted from the output.
* **Rationale:** This ensures the scraper never receives partial or zero-valued target metrics that could trigger false positive alerts.

### 2.4. Configuration Hot-Reload Lifecycle
* **Decision:** **Immediate context cancellation & reset**
* **Mechanics:** 
  1. Each background collection run operates under a cancelable `context.Context` spawned by the main controller loop.
  2. Upon receiving SIGHUP or a file-watch change event, the active run context is canceled immediately (abruptly and cleanly aborting any in-flight vCenter/M365 SDK requests).
  3. The configuration pointer is swapped, and a brand-new background collection loop run is spawned immediately, resetting the 2-hour collection timer.
* **Rationale:** This ensures the exporter is immediately responsive to configuration changes without risking resource leaks, dual background runs, or waiting up to 2 hours for old runs to finish.

### 2.5. Microsoft 365 Permissions & Pagination
* **Decision:** **Explicit permission scoping & SDK pagination handling**
* **Mechanics:**
  1. The documentation and README will explicitly document that the M365 collector requires the **`Organization.Read.All`** (or `Directory.Read.All`) Microsoft Graph Application permission.
  2. The `internal/m365` package will implement full OData next link (`@odata.nextLink`) pagination handling to fetch all pages of SKUs.
* **Rationale:** This guarantees the exporter is enterprise-ready and will not truncate licenses on massive tenants with hundreds of subscription SKUs. It also builds the pagination groundwork for future Phase 2 Entra ID metrics.

### 2.6. OTLP Exporter Push Timestamping Strategy
* **Decision:** **Timestamp with the latest snapshot success time**
* **Mechanics:** While the OTLP exporter may push metrics frequently (e.g., every 15-60 seconds), each pushed metric point will be stamped with the **timestamp of the latest snapshot's success** (the time the third-party control planes were last successfully polled).
* **Rationale:** This accurately represents the data age to downstream OTLP-compatible backends (like Dynatrace, Datadog, or New Relic) and aligns with the pull-based Prometheus scraping paradigm.

---

## 3. Implementation Blueprint Updates

These decisions update the planned codebase structure as follows:

* **`internal/license/collector.go`:** Needs to maintain a cancelable context per run, and support a "cold start" state where the initial `Snapshot` is empty (containing no target samples).
* **`internal/license/otlp.go`:** The push loop must extract the `LastSuccessTimestamp` from the snapshot and apply it as the metric point timestamp.
* **`internal/vmware/source.go`:** The `Collect` method must handle login, property fetch, and logout stateless sequence under context.
* **`internal/m365/source.go`:** The `Collect` method must handle iterative page fetches using the Graph SDK's page iterator or custom nextPageToken loop under context.
