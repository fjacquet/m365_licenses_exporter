package m365

import (
	"testing"

	"github.com/microsoftgraph/msgraph-sdk-go/models"
)

func ptr[T any](v T) *T { return &v }

func TestSkusToSamples(t *testing.T) {
	sku := models.NewSubscribedSku()
	sku.SetSkuPartNumber(ptr("SPE_E5"))
	sku.SetConsumedUnits(ptr(int32(242)))
	detail := models.NewLicenseUnitsDetail()
	detail.SetEnabled(ptr(int32(250)))
	sku.SetPrepaidUnits(detail)

	samples := skusToSamples("tenant-a", []models.SubscribedSkuable{sku})

	var gotTotal, gotUsed float64
	var product, unit string
	for _, s := range samples {
		for _, l := range s.Labels {
			if l.Key == "product" {
				product = l.Value
			}
			if l.Key == "unit" {
				unit = l.Value
			}
		}
		switch s.Name {
		case "license_seats_total":
			gotTotal = s.Value
		case "license_seats_used":
			gotUsed = s.Value
		}
	}
	if gotTotal != 250 || gotUsed != 242 {
		t.Fatalf("total=%v used=%v, want 250/242", gotTotal, gotUsed)
	}
	if product != "SPE_E5" || unit != "users" {
		t.Fatalf("product=%q unit=%q", product, unit)
	}
}

func TestSkusToSamplesNilGuards(t *testing.T) {
	sku := models.NewSubscribedSku() // all fields nil
	samples := skusToSamples("tenant-a", []models.SubscribedSkuable{sku})
	// No panics; with no counts, no seats emitted (absent-not-zero).
	for _, s := range samples {
		if s.Name == "license_seats_total" || s.Name == "license_seats_used" {
			t.Fatalf("emitted %s from a nil-count SKU", s.Name)
		}
	}
}
