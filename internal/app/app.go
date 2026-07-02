// Package app wires config, collectors, the snapshot store, and the two export
// paths into a running exporter.
package app

import (
	"context"
	"fmt"
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

// Run wires and starts the exporter. If once is true, it runs a single collection
// cycle and returns (used by --once); otherwise it serves until ctx is canceled.
func Run(ctx context.Context, cfg *config.Config, version, addr string, once bool) error {
	sources, err := BuildSources(cfg)
	if err != nil {
		return err
	}
	store := license.NewSnapshotStore(license.ColdStartSnapshot(version, runtime.Version()))
	collector := license.NewCollector(sources, store, version, runtime.Version(), 0, time.Now)

	if once {
		snap := collector.CollectOnce(ctx)
		dumpSamples(snap)
		return nil
	}

	reg := prometheus.NewRegistry()
	reg.MustRegister(license.NewPromCollector(store))

	instanceID, _ := os.Hostname()
	if instanceID == "" {
		instanceID = "unknown"
	}
	shutdownOTLP, err := setupOTLP(ctx, cfg.OTLP, version, instanceID, store)
	if err != nil {
		return err
	}
	defer func() { _ = shutdownOTLP(context.Background()) }()

	health := &Health{}
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	mux.Handle("/health", health)

	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		logrus.WithField("addr", addr).Info("serving /metrics and /health")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logrus.WithError(err).Fatal("http server failed")
		}
	}()

	// First cycle, then mark ready; then loop until ctx done.
	collector.CollectOnce(ctx)
	health.SetReady()
	go collector.Run(ctx, cfg.Collection.Interval)

	<-ctx.Done()
	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(shutCtx)
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
