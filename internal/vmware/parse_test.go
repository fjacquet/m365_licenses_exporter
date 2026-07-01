package vmware

import (
	"testing"
	"time"

	"github.com/vmware/govmomi/vim25/types"
	vimxml "github.com/vmware/govmomi/vim25/xml"
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

// TestExpirationDateXMLDecodeEndToEnd proves the real wire path: a canned XML
// payload for LicenseManagerLicenseInfo (as vCenter's SOAP response would
// contain it) is decoded through govmomi's own XML decoder
// (vim25/xml.Unmarshal, the drop-in that resolves xsi:type), and only THEN is
// the resulting struct fed into licensesToSamples. This is distinct from
// TestLimitedLicenseEmitsTotalUsedExpiration above, which hand-builds the
// types.KeyAnyValue{Value: time.Time{...}} natively and never exercises XML
// unmarshaling. parse.go's expiration() does a bare `p.Value.(time.Time)`
// type assertion; this test confirms that assertion holds for values as
// actually produced by govmomi's decoder, not just as constructed by hand in
// Go source.
func TestExpirationDateXMLDecodeEndToEnd(t *testing.T) {
	const wantExpiration = "2027-01-01T00:00:00Z"

	data := []byte(`<LicenseManagerLicenseInfo xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">
  <licenseKey>00000-00000-00000-00000-00000</licenseKey>
  <editionKey>eval</editionKey>
  <name>vSphere 8 Enterprise Plus</name>
  <total>512</total>
  <used>420</used>
  <costUnit>cpuPackage</costUnit>
  <properties>
    <key>expirationDate</key>
    <value xsi:type="xsd:dateTime">` + wantExpiration + `</value>
  </properties>
</LicenseManagerLicenseInfo>`)

	var info types.LicenseManagerLicenseInfo
	if err := vimxml.Unmarshal(data, &info); err != nil {
		t.Fatalf("vim25/xml.Unmarshal: %v", err)
	}

	// Empirically confirm the wire->Go type BEFORE trusting parse.go's
	// assertion: govmomi's decoder must have resolved xsi:type="xsd:dateTime"
	// to a native time.Time, exactly what expiration() asserts against.
	if len(info.Properties) != 1 || info.Properties[0].Key != "expirationDate" {
		t.Fatalf("expected one decoded expirationDate property, got: %+v", info.Properties)
	}
	decodedValue := info.Properties[0].Value
	tm, ok := decodedValue.(time.Time)
	if !ok {
		t.Fatalf("decoded expirationDate value has dynamic type %T, want time.Time (value=%#v) -- parse.go's expiration() type assertion would fail against real wire data", decodedValue, decodedValue)
	}

	wantTime, err := time.Parse(time.RFC3339, wantExpiration)
	if err != nil {
		t.Fatalf("parse wantExpiration: %v", err)
	}
	if !tm.Equal(wantTime) {
		t.Fatalf("decoded expirationDate = %v, want %v", tm, wantTime)
	}

	// Now run the decoded struct through the real production path.
	sv := view("vcsa01", []types.LicenseManagerLicenseInfo{info})
	s, ok := find(sv, "license_expiration_timestamp_seconds")
	if !ok {
		t.Fatal("license_expiration_timestamp_seconds sample not emitted for XML-decoded license info")
	}
	if want := float64(wantTime.Unix()); s.value != want {
		t.Fatalf("license_expiration_timestamp_seconds = %v, want %v", s.value, want)
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
