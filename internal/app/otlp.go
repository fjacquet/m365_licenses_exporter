package app

import (
	"context"
	"fmt"
	"time"

	"github.com/fjacquet/licenses_exporter/internal/config"
	"github.com/fjacquet/licenses_exporter/internal/license"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

// otlpPushInterval is the periodic reader cadence. License data is near-static;
// the push cadence only affects freshness of the cached snapshot downstream.
const otlpPushInterval = 60 * time.Second

// setupOTLP wires the OTLP/gRPC push exporter when cfg.Endpoint is set. It returns
// a shutdown func that is ALWAYS non-nil (a no-op when OTLP is disabled), so callers
// can defer it unconditionally.
func setupOTLP(ctx context.Context, cfg config.OTLPConfig, version, instanceID string, store *license.SnapshotStore) (func(context.Context) error, error) {
	if cfg.Endpoint == "" {
		return func(context.Context) error { return nil }, nil
	}

	opts := []otlpmetricgrpc.Option{otlpmetricgrpc.WithEndpoint(cfg.Endpoint)}
	if cfg.Insecure {
		opts = append(opts, otlpmetricgrpc.WithInsecure())
	}
	exporter, err := otlpmetricgrpc.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("otlp exporter: %w", err)
	}

	reader := sdkmetric.NewPeriodicReader(exporter, sdkmetric.WithInterval(otlpPushInterval))
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(license.Resource(version, instanceID)),
		sdkmetric.WithReader(reader),
	)

	if err := license.RegisterOTLP(mp.Meter("licenses_exporter"), store); err != nil {
		_ = mp.Shutdown(ctx)
		return nil, fmt.Errorf("register otlp gauges: %w", err)
	}
	return mp.Shutdown, nil
}
