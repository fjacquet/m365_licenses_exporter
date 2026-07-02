package license

import (
	"context"
	"time"

	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
)

// Collector runs the background collection loop and publishes snapshots.
type Collector struct {
	sources   []Source
	store     *SnapshotStore
	version   string
	goVersion string
	limit     int
	now       func() time.Time
}

func NewCollector(sources []Source, store *SnapshotStore, version, goVersion string, limit int, now func() time.Time) *Collector {
	if limit <= 0 {
		limit = len(sources)
	}
	return &Collector{sources: sources, store: store, version: version, goVersion: goVersion, limit: limit, now: now}
}

type sourceResult struct {
	vendor, instance string
	samples          []Sample
	duration         time.Duration
	ok               bool
}

// CollectOnce fans out over every source, builds one snapshot, publishes it, and
// returns it. A per-source failure degrades to license_up=0 (no seats emitted).
func (c *Collector) CollectOnce(ctx context.Context) *Snapshot {
	results := make([]sourceResult, len(c.sources))

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(c.limit)
	// Each goroutine writes only its own results[i]; errgroup.Wait() establishes
	// the happens-before before results is read below, so no lock is needed.
	for i, src := range c.sources {
		g.Go(func() error {
			start := c.now()
			samples, err := src.Collect(gctx)
			dur := c.now().Sub(start)
			results[i] = sourceResult{vendor: src.Vendor(), instance: src.Instance(), samples: samples, duration: dur, ok: err == nil}
			if err != nil {
				logrus.WithFields(logrus.Fields{"vendor": src.Vendor(), "instance": src.Instance()}).WithError(err).Warn("source collection failed")
			}
			return nil // never abort the cycle on one source's failure
		})
	}
	_ = g.Wait()

	nowUnix := float64(c.now().Unix())
	out := make([]Sample, 0, 16)
	out = append(out, BuildInfoSample(c.version, c.goVersion))
	for _, r := range results {
		out = append(out, ScrapeDurationSample(r.vendor, r.instance, r.duration.Seconds()))
		if r.ok {
			out = append(out, r.samples...)
			out = append(out, UpSample(r.vendor, r.instance, true))
			out = append(out, LastSuccessSample(r.vendor, r.instance, nowUnix))
		} else {
			out = append(out, UpSample(r.vendor, r.instance, false))
		}
	}
	snap := &Snapshot{Samples: out}
	c.store.Swap(snap)
	return snap
}

// Run collects immediately, then every interval until ctx is canceled. It is a
// thin convenience over CollectOnce + RunTicker for callers that want the leading
// collect folded in.
func (c *Collector) Run(ctx context.Context, interval time.Duration) {
	c.CollectOnce(ctx)
	c.RunTicker(ctx, interval)
}

// RunTicker collects every interval until ctx is canceled. Unlike Run it does NOT
// collect immediately on entry — the caller is expected to have already run the
// first cycle (e.g. Server.RunCollection collects once to mark health ready, then
// hands off to RunTicker). This avoids a double back-to-back initial collect —
// two vCenter logins / two Graph fetches — on every startup and reload.
func (c *Collector) RunTicker(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.CollectOnce(ctx)
		}
	}
}
