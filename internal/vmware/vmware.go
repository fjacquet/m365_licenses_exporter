package vmware

import (
	"fmt"

	"github.com/fjacquet/licenses_exporter/internal/config"
	"github.com/fjacquet/licenses_exporter/internal/license"
)

// NewSources builds one stateless Source per configured vCenter.
func NewSources(cfg config.VMwareRaw) ([]license.Source, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	var out []license.Source
	for _, v := range cfg.VCenters {
		pw, err := config.ResolveSecret(v.Password, v.PasswordFile)
		if err != nil {
			return nil, fmt.Errorf("vcenter %q: %w", v.Instance, err)
		}
		out = append(out, &source{
			instance: v.Instance,
			host:     v.Host,
			username: v.Username,
			password: pw,
			insecure: v.InsecureSkipVerify,
		})
	}
	return out, nil
}
