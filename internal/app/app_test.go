package app

import (
	"testing"
	"time"

	"github.com/fjacquet/licenses_exporter/internal/config"
)

func TestBuildSourcesRespectsEnabledFlags(t *testing.T) {
	cfg := &config.Config{
		Collection: config.CollectionConfig{Interval: time.Hour},
		Collectors: config.CollectorsConfig{
			VMware: config.VMwareRaw{Enabled: true, VCenters: []config.VCenterRaw{{Instance: "v1", Host: "https://vc/sdk", Username: "u", Password: "p"}}},
			M365:   config.M365Raw{Enabled: false},
		},
	}
	sources, err := BuildSources(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 1 || sources[0].Vendor() != "vmware" {
		t.Fatalf("expected 1 vmware source, got %d", len(sources))
	}
}
