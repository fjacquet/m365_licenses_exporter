package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fjacquet/licenses_exporter/internal/app"
	"github.com/fjacquet/licenses_exporter/internal/config"
	"github.com/fsnotify/fsnotify"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

// version is injected via -ldflags at build time (see Makefile `make cli`).
var version = "dev"

func main() {
	var (
		cfgPath string
		debug   bool
		once    bool
		trace   bool
		addr    string
	)
	root := &cobra.Command{
		Use:   "licenses_exporter",
		Short: "Unified enterprise-license Prometheus + OTLP exporter",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if debug {
				logrus.SetLevel(logrus.DebugLevel)
			}
			if trace {
				logrus.Warn("--trace: both collectors use non-injectable SDKs; SDK-level tracing is intentionally not wired (would leak tokens). See docs/adr.")
			}
			cfg, err := config.Load(cfgPath)
			if err != nil {
				return err
			}
			if once {
				return app.RunOnce(context.Background(), cfg, version, debug)
			}
			return serveWithReload(cfgPath, version, addr)
		},
	}
	root.Flags().StringVar(&cfgPath, "config", "config.yaml", "path to config.yaml")
	root.Flags().StringVar(&addr, "web.listen-address", ":9105", "metrics listen address")
	root.Flags().BoolVar(&debug, "debug", false, "debug logging")
	root.Flags().BoolVar(&once, "once", false, "run one collection cycle and exit")
	root.Flags().BoolVar(&trace, "trace", false, "log repo-owned API responses (SDK tracing intentionally disabled)")
	if err := root.Execute(); err != nil {
		logrus.WithError(err).Fatal("exporter failed")
	}
}

// serveWithReload builds the process-lifetime serving stack ONCE (app.NewServer:
// shared SnapshotStore + OTLP + a single bound HTTP listener) and then drives the
// reload state machine (app.Server.ReloadLoop) against it. On SIGHUP or a config
// file change the running collection loop is canceled and respawned with the new
// config on the SAME server/store — the listener is never rebound and /metrics
// keeps serving the last-good snapshot throughout (ADR-0008). A config that fails
// to load or validate on reload is REJECTED and logged; the running server is left
// untouched and never crashes. SIGINT/SIGTERM shut down cleanly.
//
// Only the initial config.Load and initial NewServer bind are fatal (startup).
// Reload never re-runs a top-of-loop config.Load that could return-and-Fatal:
// candidate configs are validated inside ReloadLoop and an invalid one is skipped.
func serveWithReload(cfgPath, version, addr string) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err // initial load failure is fatal
	}
	srv, err := app.NewServer(cfg, version, addr)
	if err != nil {
		return err // initial bind / OTLP setup failure is fatal
	}
	defer func() {
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM)

	watcher, werr := fsnotify.NewWatcher()
	if werr != nil {
		// File-watch is best-effort: without it, reload still works via SIGHUP.
		logrus.WithError(werr).Warn("config file watcher unavailable; reload via SIGHUP only")
	} else {
		defer func() { _ = watcher.Close() }()
		if err := watcher.Add(cfgPath); err != nil {
			logrus.WithError(err).WithField("file", cfgPath).Warn("cannot watch config file; reload via SIGHUP only")
		}
	}
	events := watcherEvents(watcher) // hoisted once; rebuilt-per-select was wasteful
	errs := watcherErrors(watcher)   // drained below so watcher errors are surfaced, not lost

	// Adapt OS signals + file events into plain reload/shutdown triggers so the
	// reload state machine (ReloadLoop) stays free of signal/fsnotify types and is
	// unit-testable. Non-blocking sends coalesce bursts (a pending trigger already
	// covers the reload) and keep this goroutine from ever blocking.
	reloads := make(chan struct{}, 1)
	shutdown := make(chan struct{}, 1)
	go func() {
		for {
			select {
			case sig := <-sigs:
				if sig == syscall.SIGHUP {
					logrus.Info("SIGHUP: reloading config")
					select {
					case reloads <- struct{}{}:
					default:
					}
				} else {
					select {
					case shutdown <- struct{}{}:
					default:
					}
					return // SIGINT/SIGTERM: stop translating
				}
			case ev := <-events:
				if ev.Op&(fsnotify.Write|fsnotify.Create) == 0 {
					continue
				}
				logrus.WithField("file", ev.Name).Info("config changed: reloading")
				select {
				case reloads <- struct{}{}:
				default:
				}
			case werr := <-errs:
				// Surface watcher errors instead of leaving them to pile up in the
				// channel; file-watch degrades but SIGHUP reload still works.
				logrus.WithError(werr).Warn("config file watcher error")
			}
		}
	}()

	srv.ReloadLoop(cfg, reloads, shutdown, func() (*config.Config, error) {
		return config.Load(cfgPath)
	})
	return nil
}

func watcherEvents(w *fsnotify.Watcher) <-chan fsnotify.Event {
	if w == nil {
		return make(chan fsnotify.Event) // never fires
	}
	return w.Events
}

func watcherErrors(w *fsnotify.Watcher) <-chan error {
	if w == nil {
		return make(chan error) // never fires
	}
	return w.Errors
}
