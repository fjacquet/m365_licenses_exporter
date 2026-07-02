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

// TestLabelKeyInvariantPerMetricName exercises the real mechanism the
// label-key invariant protects, rather than merely re-deriving compile-time
// constants from the Sample constructors. It runs a PromCollector over a
// SnapshotStore and asserts on what reg.Gather() actually produces.
//
// A negative case was investigated: reg.Gather() returning a non-nil error
// when a license_seats_total sample with a DIFFERENT label-key set reaches
// the same PromCollector as other license_seats_total samples. This does
// NOT happen with client_golang v1.23.2, and it cannot be made to happen for
// this collector: PromCollector.Describe (prometheus.go) intentionally
// sends no descriptors, so every metric it emits travels client_golang's
// "unchecked" collection path. In registry.go's Gather(), metrics taken
// from the unchecked channel are always passed a nil registeredDescIDs to
// processMetric, which is what disables checkDescConsistency (the check
// that would otherwise catch a label-set/help mismatch against a
// registered Desc) -- this is unconditional on that code path and does not
// change even under prometheus.NewPedanticRegistry(). This was confirmed
// empirically with a standalone reproduction against the same
// client_golang version before writing this test, and matches an explicit
// comment in prometheus/internal/metric.go's MetricSorter.Less ("This
// should not happen. The metrics are inconsistent. However, we have to
// deal with the fact... "), which exists precisely because client_golang
// tolerates dimensionally-inconsistent metrics from unchecked collectors
// instead of erroring on them.
//
// Given that, the real, falsifiable guard here is exact-content: prove
// PromCollector round-trips each sample's own label set untouched, for
// both well-formed (same key set, different vendors) and adversarial
// (different key set within one metric name) snapshots. If PromCollector
// ever started sharing/caching a Desc or a label slice across samples of
// the same metric name -- the actual bug class this invariant guards
// against -- these assertions would fail (either the exact text would stop
// matching, or MustNewConstMetric would panic on a label-count mismatch).
func TestLabelKeyInvariantPerMetricName(t *testing.T) {
	t.Run("same key set, different vendors, gathers without error", func(t *testing.T) {
		store := NewSnapshotStore(&Snapshot{Samples: []Sample{
			SeatSample(MetricSeatsTotal, "vmware", "vSphere_ENT+", "cores", "vcsa01", 512),
			SeatSample(MetricSeatsTotal, "microsoft", "M365_E5", "users", "tenant-a", 250),
		}})
		reg := prometheus.NewRegistry()
		reg.MustRegister(NewPromCollector(store))

		if _, err := reg.Gather(); err != nil {
			t.Fatalf("Gather() on well-formed multi-vendor samples returned an error: %v", err)
		}

		expected := `
# HELP license_seats_total Total license capacity purchased.
# TYPE license_seats_total gauge
license_seats_total{instance="tenant-a",product="M365_E5",unit="users",vendor="microsoft"} 250
license_seats_total{instance="vcsa01",product="vSphere_ENT+",unit="cores",vendor="vmware"} 512
`
		if err := testutil.GatherAndCompare(reg, strings.NewReader(expected), MetricSeatsTotal); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("mismatched key set within one metric name is emitted verbatim per sample", func(t *testing.T) {
		// Deliberately different key set from SeatSample's
		// {instance,product,unit,vendor} for the same metric name.
		mismatched := Sample{
			Name:   MetricSeatsTotal,
			Labels: []Label{{Key: "weird_dimension", Value: "x"}},
			Value:  99,
		}
		store := NewSnapshotStore(&Snapshot{Samples: []Sample{
			SeatSample(MetricSeatsTotal, "vmware", "vSphere_ENT+", "cores", "vcsa01", 512),
			mismatched,
		}})
		reg := prometheus.NewRegistry()
		reg.MustRegister(NewPromCollector(store))

		if _, err := reg.Gather(); err != nil {
			t.Fatalf("Gather() on mismatched-dimension samples returned an error (see comment above on why this is not expected): %v", err)
		}

		expected := `
# HELP license_seats_total Total license capacity purchased.
# TYPE license_seats_total gauge
license_seats_total{weird_dimension="x"} 99
license_seats_total{instance="vcsa01",product="vSphere_ENT+",unit="cores",vendor="vmware"} 512
`
		if err := testutil.GatherAndCompare(reg, strings.NewReader(expected), MetricSeatsTotal); err != nil {
			t.Fatal(err)
		}
	})
}
