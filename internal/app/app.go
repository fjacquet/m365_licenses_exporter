// Package app wires config, collectors, the snapshot store, and the two export
// paths into a running exporter.
package app

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"runtime"
	"sort"
	"time"

	"github.com/fjacquet/licenses_exporter/internal/config"
	"github.com/fjacquet/licenses_exporter/internal/license"
	"github.com/fjacquet/licenses_exporter/internal/m365"
	"github.com/fjacquet/licenses_exporter/internal/vmware"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
)

// BuildSources concatenates every enabled collector's sources.
func BuildSources(cfg *config.Config) ([]license.Source, error) {
	var sources []license.Source
	vm, err := vmware.NewSources(cfg.Collectors.VMware)
	if err != nil {
		return nil, err
	}
	sources = append(sources, vm...)
	ms, err := m365.NewSources(cfg.Collectors.M365)
	if err != nil {
		return nil, err
	}
	sources = append(sources, ms...)
	return sources, nil
}

// RunOnce runs a single collection cycle against a throwaway store and returns
// (the --once path). Samples are dumped (sorted, exposition style) ONLY when
// debug is true. No server is started.
func RunOnce(ctx context.Context, cfg *config.Config, version string, debug bool) error {
	sources, err := BuildSources(cfg)
	if err != nil {
		return err
	}
	store := license.NewSnapshotStore(license.ColdStartSnapshot(version, runtime.Version()))
	collector := license.NewCollector(sources, store, version, runtime.Version(), 0, time.Now)
	snap := collector.CollectOnce(ctx)
	if debug {
		dumpSamples(snap)
	}
	return nil
}

// Server is the process-lifetime serving stack: one shared SnapshotStore, the
// Prometheus registry+collector, the OTLP push exporter, the /health handler, and
// a single bound HTTP server. It is built ONCE (NewServer) and reused across every
// reload — RunCollection swaps only the collection loop, never the store or the
// listener, so /metrics never blanks and /health never flips back to 503 on a
// reload (ADR-0008 §4).
type Server struct {
	srv          *http.Server
	ln           net.Listener
	store        *license.SnapshotStore
	health       *Health
	shutdownOTLP func(context.Context) error
	version      string
	// buildSources builds the collection sources from a config. It is a field so
	// tests can inject fake sources; production wiring uses BuildSources.
	buildSources func(*config.Config) ([]license.Source, error)
}

// NewServer builds and starts the process-lifetime serving stack. The listener is
// bound up front via net.Listen so a bind failure (e.g. address in use) is
// returned synchronously — fatal only at startup, which is acceptable. Once
// serving, a runtime serve error is LOGGED, never fatal: a reload must never be
// able to kill the process.
func NewServer(cfg *config.Config, version, addr string) (*Server, error) {
	store := license.NewSnapshotStore(license.ColdStartSnapshot(version, runtime.Version()))

	reg := prometheus.NewRegistry()
	reg.MustRegister(license.NewPromCollector(store))

	instanceID, _ := os.Hostname()
	if instanceID == "" {
		instanceID = "unknown"
	}
	shutdownOTLP, err := setupOTLP(context.Background(), cfg.OTLP, version, instanceID, store)
	if err != nil {
		return nil, err
	}

	health := &Health{}
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	mux.Handle("/health", health)

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		_ = shutdownOTLP(context.Background())
		return nil, fmt.Errorf("listen %q: %w", addr, err)
	}

	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	s := &Server{
		srv:          srv,
		ln:           ln,
		store:        store,
		health:       health,
		shutdownOTLP: shutdownOTLP,
		version:      version,
		buildSources: BuildSources,
	}
	go func() {
		logrus.WithField("addr", addr).Info("serving /metrics and /health")
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			logrus.WithError(err).Error("http server failed")
		}
	}()
	return s, nil
}

// RunCollection builds the sources+collector for cfg and runs the collection loop
// into the SHARED store until ctx is canceled. It collects exactly once, marks
// health ready after that first cycle, then hands off to the ticker-only loop —
// never a double initial collect. Because it publishes into the existing shared
// store, the previous snapshot keeps serving until this loop's first CollectOnce
// swaps a fresh one, so /metrics never blanks across a reload. Reloads call this
// repeatedly on the same *Server.
func (s *Server) RunCollection(ctx context.Context, cfg *config.Config) error {
	sources, err := s.buildSources(cfg)
	if err != nil {
		return err
	}
	collector := license.NewCollector(sources, s.store, s.version, runtime.Version(), 0, time.Now)
	collector.CollectOnce(ctx)
	s.health.SetReady()
	collector.RunTicker(ctx, cfg.Collection.Interval)
	return nil
}

// ReloadLoop is the reload state machine. It runs one collection loop under a
// cancelable context, then on each reload trigger validates a candidate config
// (via load) and — only if it loads/validates — cancels the running loop and
// respawns collection with the new config on the SAME server/store. A candidate
// that fails to load is logged and skipped: the running collection and server are
// left untouched. A shutdown trigger cancels the active loop and returns.
//
// main.go adapts OS signals (SIGHUP → reloads, SIGINT/SIGTERM → shutdown) and
// fsnotify write/create events (→ reloads) into the two trigger channels; keeping
// them as plain channels makes this loop testable without real signals or files.
func (s *Server) ReloadLoop(initialCfg *config.Config, reloads, shutdown <-chan struct{}, load func() (*config.Config, error)) {
	cfg := initialCfg
	for {
		ctx, cancel := context.WithCancel(context.Background())
		go func(cfg *config.Config) {
			if err := s.RunCollection(ctx, cfg); err != nil {
				logrus.WithError(err).Error("collection cycle ended")
			}
		}(cfg)

		var newCfg *config.Config
		for newCfg == nil {
			select {
			case <-shutdown:
				cancel() // stop the active collection loop and exit
				return
			case <-reloads:
				// Validate the candidate BEFORE tearing down the running loop.
				loaded, err := load()
				if err != nil {
					logrus.WithError(err).Warn("new config invalid; keeping current running config")
					continue
				}
				newCfg = loaded
			}
		}
		cancel() // tear down old loop; outer loop respawns with the new, validated config
		cfg = newCfg
	}
}

// Shutdown gracefully stops the HTTP server and the OTLP exporter.
func (s *Server) Shutdown(ctx context.Context) error {
	err := s.srv.Shutdown(ctx)
	if s.shutdownOTLP != nil {
		if oerr := s.shutdownOTLP(ctx); oerr != nil && err == nil {
			err = oerr
		}
	}
	return err
}

// dumpSamples prints every sample sorted (exposition style) for --once --debug.
func dumpSamples(snap *license.Snapshot) {
	lines := make([]string, 0, len(snap.Samples))
	for _, s := range snap.Samples {
		lines = append(lines, fmt.Sprintf("%s %g", s.Name, s.Value))
	}
	sort.Strings(lines)
	for _, l := range lines {
		fmt.Println(l)
	}
}
