package vmware

import (
	"time"

	"github.com/fjacquet/licenses_exporter/internal/license"
	"github.com/vmware/govmomi/vim25/types"
)

const vendor = "vmware"

// licensesToSamples maps vSphere LicenseManager entries to license samples.
// Unlimited licenses (Total <= 0) omit seats_total (absent-not-zero).
func licensesToSamples(instance string, infos []types.LicenseManagerLicenseInfo) []license.Sample {
	var out []license.Sample
	for _, info := range infos {
		unit := info.CostUnit
		if unit == "" {
			unit = "unit"
		}
		product := info.Name
		if info.Total > 0 {
			out = append(out, license.SeatSample(license.MetricSeatsTotal, vendor, product, unit, instance, float64(info.Total)))
		}
		out = append(out, license.SeatSample(license.MetricSeatsUsed, vendor, product, unit, instance, float64(info.Used)))
		if exp, ok := expiration(info.Properties); ok {
			out = append(out, license.ExpirationSample(vendor, product, instance, float64(exp.Unix())))
		}
	}
	return out
}

// expiration extracts the expirationDate property; absent for perpetual licenses.
func expiration(props []types.KeyAnyValue) (time.Time, bool) {
	for _, p := range props {
		if p.Key != "expirationDate" {
			continue
		}
		if t, ok := p.Value.(time.Time); ok {
			return t, true
		}
	}
	return time.Time{}, false
}
