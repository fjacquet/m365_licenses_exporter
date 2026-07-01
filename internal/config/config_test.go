package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeTemp(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadExpandsEnvAndParsesInterval(t *testing.T) {
	t.Setenv("VC_PASS", "s3cret")
	p := writeTemp(t, `
collection:
  interval: 2h
collectors:
  vmware:
    enabled: true
    vcenters:
      - instance: vcsa01
        host: https://vc/sdk
        username: svc
        password: ${VC_PASS}
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Collection.Interval != 2*time.Hour {
		t.Fatalf("interval = %v, want 2h", cfg.Collection.Interval)
	}
	if got := cfg.Collectors.VMware.VCenters[0].Password; got != "s3cret" {
		t.Fatalf("password = %q, want expanded", got)
	}
}

func TestLoadFailsOnUnsetEnv(t *testing.T) {
	p := writeTemp(t, `
collection: { interval: 1h }
collectors:
  vmware:
    enabled: true
    vcenters:
      - instance: v1
        host: h
        username: u
        password: ${DEFINITELY_UNSET_VAR}
`)
	if _, err := Load(p); err == nil {
		t.Fatal("expected error on unset env var")
	}
}
