package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

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
				return app.Run(context.Background(), cfg, version, addr, true)
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

// serveWithReload runs the exporter under a cancelable context and rebuilds it on
// SIGHUP or config file change (design spec §2). A config that fails to load or
// validate on reload is REJECTED: the currently running server is left
// untouched and serveWithReload keeps waiting for the next reload trigger.
//
// Deviation from the task-9 brief: the brief's version re-ran config.Load at
// the top of its single `for {}` loop and treated any error there as fatal
// (`return err`). Because a rejected reload's `continue` falls straight back
// to that same top-of-loop Load against the very same (still-invalid) file, it
// would immediately return that error and crash the whole process via main's
// `logrus.Fatal` — contradicting the "REJECTED; keep the running server"
// requirement. This version only re-validates a candidate config inside the
// inner wait loop: an invalid candidate is logged and looped past without
// ever touching the running ctx/cancel/server; the outer loop only rebuilds
// once a *valid* new config has actually been obtained.
func serveWithReload(cfgPath, version, addr string) error {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM)

	watcher, _ := fsnotify.NewWatcher()
	if watcher != nil {
		defer watcher.Close()
		_ = watcher.Add(cfgPath)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err // initial load failure is fatal
	}

	for {
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			if err := app.Run(ctx, cfg, version, addr, false); err != nil {
				logrus.WithError(err).Error("run cycle ended")
			}
		}()

		var newCfg *config.Config
		for newCfg == nil {
			select {
			case sig := <-sigs:
				if sig == syscall.SIGHUP {
					logrus.Info("SIGHUP: reloading config")
				} else {
					cancel()
					return nil // SIGINT/SIGTERM: shut down
				}
			case ev := <-watcherEvents(watcher):
				if ev.Op&(fsnotify.Write|fsnotify.Create) == 0 {
					continue
				}
				logrus.WithField("file", ev.Name).Info("config changed: reloading")
			}
			// Validate the candidate config BEFORE tearing down the running server.
			loaded, err := config.Load(cfgPath)
			if err != nil {
				logrus.WithError(err).Warn("new config invalid; keeping current running config")
				continue
			}
			newCfg = loaded
		}
		cancel() // tear down old; loop rebuilds with the new, already-validated config
		cfg = newCfg
	}
}

func watcherEvents(w *fsnotify.Watcher) <-chan fsnotify.Event {
	if w == nil {
		return make(chan fsnotify.Event) // never fires
	}
	return w.Events
}
