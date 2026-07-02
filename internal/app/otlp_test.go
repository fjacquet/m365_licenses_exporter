package app

import (
	"context"
	"testing"
	"time"

	"github.com/fjacquet/licenses_exporter/internal/config"
	"github.com/fjacquet/licenses_exporter/internal/license"
)

func TestSetupOTLPDisabledWhenNoEndpoint(t *testing.T) {
	store := license.NewSnapshotStore(license.ColdStartSnapshot("v", "go"))
	shutdown, err := setupOTLP(context.Background(), config.OTLPConfig{}, "v", "id", store)
	if err != nil {
		t.Fatalf("disabled setup should not error: %v", err)
	}
	if shutdown == nil {
		t.Fatal("shutdown must be non-nil even when disabled")
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("no-op shutdown should not error: %v", err)
	}
}

func TestSetupOTLPEnabledConstructsProvider(t *testing.T) {
	store := license.NewSnapshotStore(license.ColdStartSnapshot("v", "go"))
	// Construction must succeed without a live collector (grpc dials lazily).
	shutdown, err := setupOTLP(context.Background(), config.OTLPConfig{Endpoint: "127.0.0.1:4317", Insecure: true}, "v", "id", store)
	if err != nil {
		t.Fatalf("enabled setup should construct without error: %v", err)
	}
	if shutdown == nil {
		t.Fatal("shutdown must be non-nil")
	}
	// Bounded shutdown so the test never hangs on a dead endpoint; a dial-timeout
	// error here is acceptable (we only require setup succeeded and shutdown returns).
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = shutdown(ctx)
}
