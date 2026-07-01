package license

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeSource struct {
	vendor, instance string
	samples          []Sample
	err              error
}

func (f fakeSource) Vendor() string   { return f.vendor }
func (f fakeSource) Instance() string { return f.instance }
func (f fakeSource) Collect(context.Context) ([]Sample, error) {
	return f.samples, f.err
}

func countByName(samples []Sample, name string) int {
	n := 0
	for _, s := range samples {
		if s.Name == name {
			n++
		}
	}
	return n
}

func upValue(t *testing.T, samples []Sample, vendor, instance string) float64 {
	t.Helper()
	for _, s := range samples {
		if s.Name != MetricUp {
			continue
		}
		v, _ := labelValue(s, "vendor")
		i, _ := labelValue(s, "instance")
		if v == vendor && i == instance {
			return s.Value
		}
	}
	t.Fatalf("no license_up for %s/%s", vendor, instance)
	return -1
}

func fixedClock() time.Time { return time.Unix(1_700_000_000, 0) }

func TestCollectOnceMergesHealthyAndFailedSources(t *testing.T) {
	good := fakeSource{
		vendor: "vmware", instance: "vcsa01",
		samples: []Sample{SeatSample(MetricSeatsTotal, "vmware", "p", "cpuPackage", "vcsa01", 512)},
	}
	bad := fakeSource{vendor: "microsoft", instance: "tenant-a", err: errors.New("boom")}

	store := NewSnapshotStore(ColdStartSnapshot("v", "go"))
	c := NewCollector([]Source{good, bad}, store, "v", "go", 4, fixedClock)
	snap := c.CollectOnce(context.Background())

	if got := upValue(t, snap.Samples, "vmware", "vcsa01"); got != 1 {
		t.Fatalf("good up = %v, want 1", got)
	}
	if got := upValue(t, snap.Samples, "microsoft", "tenant-a"); got != 0 {
		t.Fatalf("bad up = %v, want 0", got)
	}
	// Failed source must NOT emit any seats (absent-not-zero).
	for _, s := range snap.Samples {
		if s.Name == MetricSeatsTotal {
			if v, _ := labelValue(s, "vendor"); v == "microsoft" {
				t.Fatal("failed source emitted seats_total")
			}
		}
	}
	if countByName(snap.Samples, MetricBuildInfo) != 1 {
		t.Fatal("expected exactly one build_info")
	}
	// Healthy source records last_success; failed source does not.
	if countByName(snap.Samples, MetricLastSuccess) != 1 {
		t.Fatalf("last_success count = %d, want 1", countByName(snap.Samples, MetricLastSuccess))
	}
	if store.Load() != snap {
		t.Fatal("CollectOnce did not publish the snapshot")
	}
}
