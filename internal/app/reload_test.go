package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fjacquet/licenses_exporter/internal/config"
	"github.com/fjacquet/licenses_exporter/internal/license"
)

// gatedSource is a fake license.Source with deterministic coordination hooks:
// started signals (once) when Collect is entered, and release (when non-nil)
// blocks Collect until closed/received — letting a test observe the shared store
// while a reload's first CollectOnce is mid-flight.
type gatedSource struct {
	vendor, instance string
	samples          []license.Sample
	started          chan struct{} // buffered(1); nil to skip
	release          chan struct{} // nil => return immediately
}

func (g *gatedSource) Vendor() string   { return g.vendor }
func (g *gatedSource) Instance() string { return g.instance }
func (g *gatedSource) Collect(ctx context.Context) ([]license.Sample, error) {
	if g.started != nil {
		select {
		case g.started <- struct{}{}:
		default:
		}
	}
	if g.release != nil {
		select {
		case <-g.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return g.samples, nil
}

// hasUp reports whether snap carries a license_up sample for vendor (any value).
func hasUp(snap *license.Snapshot, vendor string) bool {
	if snap == nil {
		return false
	}
	for _, s := range snap.Samples {
		if s.Name != license.MetricUp {
			continue
		}
		for _, l := range s.Labels {
			if l.Key == "vendor" && l.Value == vendor {
				return true
			}
		}
	}
	return false
}

// eventually polls cond until true or the deadline elapses. It waits for a
// convergent condition (a publish that WILL happen), not a guessed race window.
func eventually(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timeout: %s", msg)
}

func cfgWithInterval(d time.Duration) *config.Config {
	return &config.Config{Collection: config.CollectionConfig{Interval: d}}
}

// TestReloadLoopStoreContinuity drives Server.ReloadLoop with fake sources and
// fake reload/shutdown triggers, asserting the ADR-0008 invariants:
//  1. after startup the shared store serves the collected samples (not build_info-only);
//  2. across a reload the store CONTINUES to serve the prior snapshot until the new
//     collection's first CollectOnce publishes (never blanks to build_info-only);
//  3. a reload whose config fails to load is rejected — store/server keep the old data;
//  4. the loop returns cleanly on a shutdown trigger (and never calls os.Exit).
func TestReloadLoopStoreContinuity(t *testing.T) {
	const longInterval = time.Hour // ticker must not fire during the test

	// Source A collects instantly; source B blocks in Collect until released.
	srcA := &gatedSource{vendor: "venA", instance: "instA", samples: []license.Sample{license.UpSample("venA", "instA", true)}}
	releaseB := make(chan struct{})
	srcB := &gatedSource{
		vendor: "venB", instance: "instB",
		samples: []license.Sample{license.UpSample("venB", "instB", true)},
		started: make(chan struct{}, 1),
		release: releaseB,
	}

	// buildSources hands out the next source set per RunCollection call.
	srcCh := make(chan []license.Source, 8)
	srcCh <- []license.Source{srcA} // initial collection uses A
	srv := &Server{
		store:        license.NewSnapshotStore(license.ColdStartSnapshot("v", "go")),
		health:       &Health{},
		version:      "v",
		buildSources: func(*config.Config) ([]license.Source, error) { return <-srcCh, nil },
	}

	// load() is driven by the test: each call reports it ran, then returns the
	// next queued (cfg, err) result.
	type loadResult struct {
		cfg *config.Config
		err error
	}
	loadResults := make(chan loadResult, 8)
	loadInvoked := make(chan struct{}, 8)
	load := func() (*config.Config, error) {
		loadInvoked <- struct{}{}
		r := <-loadResults
		return r.cfg, r.err
	}

	reloads := make(chan struct{}, 1)
	shutdown := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		srv.ReloadLoop(cfgWithInterval(longInterval), reloads, shutdown, load)
		close(done)
	}()

	// (1) Startup: the store serves A's samples, not just build_info.
	eventually(t, func() bool { return hasUp(srv.store.Load(), "venA") },
		"store never served source A after startup")
	if srv.store.Load() == nil || !srv.health.ready.Load() {
		t.Fatal("health should be ready after the first collection cycle")
	}

	// (2) Reload to B. Queue B's source set and a valid config, then trigger.
	srcCh <- []license.Source{srcB}
	loadResults <- loadResult{cfg: cfgWithInterval(longInterval)}
	reloads <- struct{}{}
	<-loadInvoked  // ReloadLoop consumed the reload and validated the candidate
	<-srcB.started // B is now blocked inside Collect: the new snapshot has NOT published

	// The store must STILL serve A (never blank to build_info-only) mid-reload.
	if snap := srv.store.Load(); !hasUp(snap, "venA") || hasUp(snap, "venB") {
		t.Fatalf("mid-reload store must still serve prior snapshot (A), got venA=%v venB=%v",
			hasUp(snap, "venA"), hasUp(snap, "venB"))
	}
	if !srv.health.ready.Load() {
		t.Fatal("health must stay ready across a reload")
	}

	// Release B; the new collection publishes and the store swaps to B.
	close(releaseB)
	eventually(t, func() bool { return hasUp(srv.store.Load(), "venB") },
		"store never swapped to source B after release")
	if hasUp(srv.store.Load(), "venA") {
		t.Fatal("after B published, store must no longer serve A")
	}

	// (3) Reject a bad reload: load() errors → store/server keep serving B.
	loadResults <- loadResult{err: errors.New("bad config")}
	reloads <- struct{}{}
	<-loadInvoked // ReloadLoop consumed and rejected the candidate
	if snap := srv.store.Load(); !hasUp(snap, "venB") {
		t.Fatal("rejected reload must leave the store serving the last-good snapshot (B)")
	}

	// (4) Shutdown returns cleanly (no os.Exit — that would kill this test process).
	shutdown <- struct{}{}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ReloadLoop did not return on shutdown trigger")
	}
}
