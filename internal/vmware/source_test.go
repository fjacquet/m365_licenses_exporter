package vmware

import (
	"context"
	"testing"

	"github.com/vmware/govmomi/simulator"
)

// vcsim's default license manager returns the eval license (Total=0). This is a
// wiring smoke test: login → List → logout must succeed, and the unlimited eval
// license must never yield a non-positive seats_total.
func TestCollectAgainstVcsim(t *testing.T) {
	model := simulator.VPX()
	if err := model.Create(); err != nil {
		t.Fatal(err)
	}
	defer model.Remove()
	server := model.Service.NewServer()
	defer server.Close()

	src := &source{instance: "vcsim", host: server.URL.String(), username: "user", password: "pass", insecure: true}
	samples, err := src.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect against vcsim: %v", err)
	}
	if len(samples) == 0 {
		t.Fatal("Collect against vcsim returned no samples (vcsim returned zero licenses?)")
	}
	sawSeatsUsed := false
	for _, s := range samples {
		if s.Name == "license_seats_total" && s.Value <= 0 {
			t.Fatalf("emitted non-positive seats_total %v (unlimited must be omitted)", s.Value)
		}
		if s.Name == "license_seats_used" {
			sawSeatsUsed = true
		}
	}
	if !sawSeatsUsed {
		t.Fatal("expected at least one license_seats_used sample from vcsim, got none")
	}
}
