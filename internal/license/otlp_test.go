package license

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestRegisterOTLPObservesSnapshot(t *testing.T) {
	store := NewSnapshotStore(&Snapshot{Samples: []Sample{
		SeatSample(MetricSeatsUsed, "microsoft", "M365_E5", "users", "tenant-a", 242),
	}})
	reader := metric.NewManualReader()
	provider := metric.NewMeterProvider(metric.WithReader(reader))
	meter := provider.Meter("licenses_exporter")
	if err := RegisterOTLP(meter, store); err != nil {
		t.Fatalf("RegisterOTLP: %v", err)
	}

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}

	var found bool
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != MetricSeatsUsed {
				continue
			}
			g, ok := m.Data.(metricdata.Gauge[float64])
			if !ok {
				t.Fatalf("%s is not a float64 gauge", m.Name)
			}
			for _, dp := range g.DataPoints {
				if dp.Value == 242 {
					found = true
				}
			}
		}
	}
	if !found {
		t.Fatal("license_seats_used=242 not observed via OTLP ManualReader")
	}
}
