package license

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// allMetricNames is the fixed set of observable gauges we register.
var allMetricNames = []string{
	MetricSeatsTotal, MetricSeatsUsed, MetricExpiration,
	MetricUp, MetricLastSuccess, MetricScrapeDuration, MetricBuildInfo,
}

// RegisterOTLP registers one observable gauge per metric name. Each callback
// reads the current snapshot and observes its matching samples at OBSERVATION
// time (points are not back-dated; data age is carried by
// license_collector_last_success_timestamp_seconds).
func RegisterOTLP(meter metric.Meter, store *SnapshotStore) error {
	for _, name := range allMetricNames {
		g, err := meter.Float64ObservableGauge(name, metric.WithDescription(helpText[name]))
		if err != nil {
			return err
		}
		_, err = meter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
			snap := store.Load()
			if snap == nil {
				return nil
			}
			for _, s := range snap.Samples {
				if s.Name != name {
					continue
				}
				attrs := make([]attribute.KeyValue, len(s.Labels))
				for i, l := range s.Labels {
					attrs[i] = attribute.String(l.Key, l.Value)
				}
				o.ObserveFloat64(g, s.Value, metric.WithAttributes(attrs...))
			}
			return nil
		}, g)
		if err != nil {
			return err
		}
	}
	return nil
}

// Resource builds the OTLP resource attributes for the exporter.
func Resource(version, instanceID string) *resource.Resource {
	return resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName("licenses_exporter"),
		semconv.ServiceVersion(version),
		semconv.ServiceInstanceID(instanceID),
	)
}
