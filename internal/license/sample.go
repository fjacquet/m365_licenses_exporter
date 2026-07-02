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
