package license

import "testing"

func labelValue(s Sample, key string) (string, bool) {
	for _, l := range s.Labels {
		if l.Key == key {
			return l.Value, true
		}
	}
	return "", false
}

func TestSeatSampleHasCanonicalLabelKeys(t *testing.T) {
	s := SeatSample(MetricSeatsTotal, "vmware", "vSphere_ENT+", "cpuPackage", "vcsa01", 512)
	if s.Name != "license_seats_total" {
		t.Fatalf("name = %q", s.Name)
	}
	if s.Value != 512 {
		t.Fatalf("value = %v", s.Value)
	}
	// Labels must be sorted by key: instance, product, unit, vendor.
	wantKeys := []string{"instance", "product", "unit", "vendor"}
	if len(s.Labels) != len(wantKeys) {
		t.Fatalf("label count = %d, want %d", len(s.Labels), len(wantKeys))
	}
	for i, k := range wantKeys {
		if s.Labels[i].Key != k {
			t.Fatalf("label[%d].Key = %q, want %q", i, s.Labels[i].Key, k)
		}
	}
	if v, _ := labelValue(s, "vendor"); v != "vmware" {
		t.Fatalf("vendor = %q", v)
	}
}

func TestUpSampleUsesVendorInstanceOnly(t *testing.T) {
	s := UpSample("microsoft", "tenant-a", false)
	if s.Name != "license_up" || s.Value != 0 {
		t.Fatalf("got %q=%v", s.Name, s.Value)
	}
	if len(s.Labels) != 2 {
		t.Fatalf("up label count = %d, want 2", len(s.Labels))
	}
	if _, ok := labelValue(s, "product"); ok {
		t.Fatal("up must not carry a product label")
	}
}
