// Package config loads and validates the exporter configuration.
package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v2"
)

type Config struct {
	Collection CollectionConfig `yaml:"collection"`
	OTLP       OTLPConfig       `yaml:"otlp"`
	Collectors CollectorsConfig `yaml:"collectors"`
}

type CollectionConfig struct {
	Interval time.Duration `yaml:"interval"`
}

// OTLPConfig configures the optional OTLP/gRPC push exporter. Endpoint empty
// disables OTLP entirely (the exporter then serves Prometheus only).
type OTLPConfig struct {
	Endpoint string `yaml:"endpoint"`
	Insecure bool   `yaml:"insecure"`
}

type CollectorsConfig struct {
	M365   M365Raw   `yaml:"m365"`
	VMware VMwareRaw `yaml:"vmware"`
}

type M365Raw struct {
	Enabled bool        `yaml:"enabled"`
	Tenants []TenantRaw `yaml:"tenants"`
}

type TenantRaw struct {
	Instance         string `yaml:"instance"`
	TenantID         string `yaml:"tenantId"`
	ClientID         string `yaml:"clientId"`
	ClientSecret     string `yaml:"clientSecret"`
	ClientSecretFile string `yaml:"clientSecretFile"`
}

type VMwareRaw struct {
	Enabled  bool         `yaml:"enabled"`
	VCenters []VCenterRaw `yaml:"vcenters"`
}

type VCenterRaw struct {
	Instance           string `yaml:"instance"`
	Host               string `yaml:"host"`
	Username           string `yaml:"username"`
	Password           string `yaml:"password"`
	PasswordFile       string `yaml:"passwordFile"`
	InsecureSkipVerify bool   `yaml:"insecureSkipVerify"`
}

var envRef = regexp.MustCompile(`\$\{([A-Z0-9_]+)\}`)

// Expand replaces ${VAR} references, failing on any unset variable.
func Expand(s string) (string, error) {
	var missing string
	out := envRef.ReplaceAllStringFunc(s, func(m string) string {
		name := envRef.FindStringSubmatch(m)[1]
		v, ok := os.LookupEnv(name)
		if !ok {
			missing = name
			return m
		}
		return v
	})
	if missing != "" {
		return "", fmt.Errorf("config references unset environment variable %q", missing)
	}
	return out, nil
}

// ResolveSecret returns the secret read from file (trimmed of surrounding
// whitespace) when file is set, otherwise the inline value. Shared by the
// vendor collectors so inline-vs-file precedence stays consistent across them.
func ResolveSecret(inline, file string) (string, error) {
	if file != "" {
		b, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("read secret file: %w", err)
		}
		return strings.TrimSpace(string(b)), nil
	}
	return inline, nil
}

// Load reads .env, expands ${ENV}, unmarshals YAML, and validates.
func Load(path string) (*Config, error) {
	LoadDotEnv(path)
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	expanded, err := Expand(string(raw))
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) validate() error {
	if c.Collection.Interval <= 0 {
		return fmt.Errorf("collection.interval must be > 0")
	}
	if !c.Collectors.M365.Enabled && !c.Collectors.VMware.Enabled {
		return fmt.Errorf("no collectors enabled")
	}
	for _, v := range c.Collectors.VMware.VCenters {
		if v.Instance == "" || v.Host == "" {
			return fmt.Errorf("vmware vcenter entry missing instance or host")
		}
	}
	for _, t := range c.Collectors.M365.Tenants {
		if t.Instance == "" || t.TenantID == "" {
			return fmt.Errorf("m365 tenant entry missing instance or tenantId")
		}
	}
	return nil
}

// UnmarshalYAML lets collection.interval accept a Go duration string ("2h").
func (c *CollectionConfig) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var raw struct {
		Interval string `yaml:"interval"`
	}
	if err := unmarshal(&raw); err != nil {
		return err
	}
	d, err := time.ParseDuration(raw.Interval)
	if err != nil {
		return fmt.Errorf("collection.interval %q: %w", raw.Interval, err)
	}
	c.Interval = d
	return nil
}
