package license

import (
	"sync"
	"testing"
)

func TestColdStartSnapshotHasOnlyBuildInfo(t *testing.T) {
	snap := ColdStartSnapshot("1.2.3", "go1.26.4")
	if len(snap.Samples) != 1 {
		t.Fatalf("cold start sample count = %d, want 1", len(snap.Samples))
	}
	if snap.Samples[0].Name != MetricBuildInfo {
		t.Fatalf("cold start metric = %q, want %q", snap.Samples[0].Name, MetricBuildInfo)
	}
}

func TestSnapshotStoreSwapAndLoad(t *testing.T) {
	store := NewSnapshotStore(ColdStartSnapshot("1.2.3", "go1.26.4"))
	next := &Snapshot{Samples: []Sample{UpSample("vmware", "vcsa01", true)}}
	store.Swap(next)
	if got := store.Load(); got != next {
		t.Fatal("Load did not return the swapped snapshot pointer")
	}
}

func TestSnapshotStoreConcurrentAccess(t *testing.T) {
	store := NewSnapshotStore(&Snapshot{})
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); store.Swap(&Snapshot{Samples: []Sample{{Name: "x"}}}) }()
		go func() { defer wg.Done(); _ = store.Load() }()
	}
	wg.Wait()
}
