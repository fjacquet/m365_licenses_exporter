package license

import "sync"

// Snapshot is an immutable set of samples produced by one collection cycle.
// Callers MUST NOT mutate Samples after publishing to a SnapshotStore.
type Snapshot struct {
	Samples []Sample
}

// ColdStartSnapshot is served before the first collection cycle completes: it
// carries only license_build_info so no target series (license_up, seats_*)
// flap or read as a transient zero (design spec §2).
func ColdStartSnapshot(version, goVersion string) *Snapshot {
	return &Snapshot{Samples: []Sample{BuildInfoSample(version, goVersion)}}
}

// SnapshotStore holds the current snapshot behind an RWMutex pointer-swap.
type SnapshotStore struct {
	mu  sync.RWMutex
	cur *Snapshot
}

func NewSnapshotStore(initial *Snapshot) *SnapshotStore {
	return &SnapshotStore{cur: initial}
}

func (s *SnapshotStore) Load() *Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cur
}

func (s *SnapshotStore) Swap(next *Snapshot) {
	s.mu.Lock()
	s.cur = next
	s.mu.Unlock()
}
