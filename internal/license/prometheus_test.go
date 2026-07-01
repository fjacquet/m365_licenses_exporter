package license

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestPromCollectorEmitsSnapshotSamples(t *testing.T) {
	store := NewSnapshotStore(&Snapshot{Samples: []Sample{
		SeatSample(MetricSeatsTotal, "microsoft", "M365_E5", "users", "tenant-a", 250),
		UpSample("microsoft", "tenant-a", true),
	}})
	reg := prometheus.NewRegistry()
	reg.MustRegister(NewPromCollector(store))

	expected := `
# HELP license_seats_total Total license capacity purchased.
# TYPE license_seats_total gauge
license_seats_total{instance="tenant-a",product="M365_E5",unit="users",vendor="microsoft"} 250
# HELP license_up 1 if the last refresh of this target succeeded, else 0.
# TYPE license_up gauge
license_up{instance="tenant-a",vendor="microsoft"} 1
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(expected),
		MetricSeatsTotal, MetricUp); err != nil {
		t.Fatal(err)
	}
}

func TestLabelKeyInvariantPerMetricName(t *testing.T) {
	// Every constructor for a given metric name must yield the same label keys.
	samples := []Sample{
		SeatSample(MetricSeatsTotal, "vmware", "a", "cores", "v1", 1),
		SeatSample(MetricSeatsTotal, "microsoft", "b", "users", "t1", 2),
		UpSample("vmware", "v1", true),
		UpSample("microsoft", "t1", false),
	}
	keysByName := map[string][]string{}
	for _, s := range samples {
		var keys []string
		for _, l := range s.Labels {
			keys = append(keys, l.Key)
		}
		joined := strings.Join(keys, ",")
		if prev, ok := keysByName[s.Name]; ok && strings.Join(prev, ",") != joined {
			t.Fatalf("metric %q has inconsistent label keys: %v vs %v", s.Name, prev, keys)
		}
		keysByName[s.Name] = keys
	}
}
