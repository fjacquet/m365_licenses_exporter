package license

import "github.com/prometheus/client_golang/prometheus"

// helpText maps each metric name to its HELP string.
var helpText = map[string]string{
	MetricSeatsTotal:     "Total license capacity purchased.",
	MetricSeatsUsed:      "Currently consumed license capacity.",
	MetricExpiration:     "License expiration as a Unix timestamp (absent when perpetual).",
	MetricUp:             "1 if the last refresh of this target succeeded, else 0.",
	MetricLastSuccess:    "Unix timestamp of the last successful collection for this target.",
	MetricScrapeDuration: "Duration of the last collection for this target, in seconds.",
	MetricBuildInfo:      "Build information; constant 1.",
}

// PromCollector is an unchecked prometheus.Collector: Describe sends nothing, so
// the emitted metric-name/label set may vary snapshot to snapshot.
type PromCollector struct {
	store *SnapshotStore
}

func NewPromCollector(store *SnapshotStore) *PromCollector { return &PromCollector{store: store} }

func (p *PromCollector) Describe(chan<- *prometheus.Desc) {} // unchecked

func (p *PromCollector) Collect(ch chan<- prometheus.Metric) {
	snap := p.store.Load()
	if snap == nil {
		return
	}
	for _, s := range snap.Samples {
		keys := make([]string, len(s.Labels))
		vals := make([]string, len(s.Labels))
		for i, l := range s.Labels {
			keys[i] = l.Key
			vals[i] = l.Value
		}
		help := helpText[s.Name]
		desc := prometheus.NewDesc(s.Name, help, keys, nil)
		ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, s.Value, vals...)
	}
}
