package vmware

import (
	"testing"
	"time"

	"github.com/vmware/govmomi/vim25/types"
)

type sampleView struct {
	name  string
	value float64
	unit  string
	prod  string
}

func find(samples []sampleView, name string) (sampleView, bool) {
	for _, s := range samples {
		if s.name == name {
			return s, true
		}
	}
	return sampleView{}, false
}

func view(instance string, infos []types.LicenseManagerLicenseInfo) []sampleView {
	out := []sampleView{}
	for _, s := range licensesToSamples(instance, infos) {
		v := sampleView{name: s.Name, value: s.Value}
		for _, l := range s.Labels {
			switch l.Key {
			case "unit":
				v.unit = l.Value
			case "product":
				v.prod = l.Value
			}
		}
		out = append(out, v)
	}
	return out
}

func TestLimitedLicenseEmitsTotalUsedExpiration(t *testing.T) {
	exp := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	infos := []types.LicenseManagerLicenseInfo{{
		Name:     "vSphere 8 Enterprise Plus",
		Total:    512,
		Used:     420,
		CostUnit: "cpuPackage",
		Properties: []types.KeyAnyValue{
			{Key: "expirationDate", Value: exp},
		},
	}}
	sv := view("vcsa01", infos)
	if s, ok := find(sv, "license_seats_total"); !ok || s.value != 512 || s.unit != "cpuPackage" {
		t.Fatalf("seats_total wrong: %+v ok=%v", s, ok)
	}
	if s, ok := find(sv, "license_seats_used"); !ok || s.value != 420 {
		t.Fatalf("seats_used wrong: %+v ok=%v", s, ok)
	}
	if s, ok := find(sv, "license_expiration_timestamp_seconds"); !ok || s.value != float64(exp.Unix()) {
		t.Fatalf("expiration wrong: %+v ok=%v", s, ok)
	}
}

func TestUnlimitedLicenseOmitsTotal(t *testing.T) {
	infos := []types.LicenseManagerLicenseInfo{{
		Name:     "Evaluation Mode",
		Total:    0, // unlimited
		Used:     3,
		CostUnit: "cpuPackage",
	}}
	sv := view("vcsa01", infos)
	if _, ok := find(sv, "license_seats_total"); ok {
		t.Fatal("unlimited license must omit seats_total")
	}
	if s, ok := find(sv, "license_seats_used"); !ok || s.value != 3 {
		t.Fatalf("seats_used wrong: %+v ok=%v", s, ok)
	}
}
