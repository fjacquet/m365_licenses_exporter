package m365

import (
	"github.com/fjacquet/licenses_exporter/internal/license"
	"github.com/microsoftgraph/msgraph-sdk-go/models"
)

const (
	vendor = "microsoft"
	unit   = "users"
)

// skusToSamples maps subscribedSkus to license samples. Every getter is
// nil-guarded: a missing count yields an absent sample, never a fake 0.
func skusToSamples(instance string, skus []models.SubscribedSkuable) []license.Sample {
	var out []license.Sample
	for _, sku := range skus {
		if sku == nil {
			continue
		}
		// A SKU with no skuPartNumber cannot be identified; emitting product=""
		// would collapse distinct such SKUs onto one series. Skip it (absent, not
		// a blank-labelled fake) per the raw-facts contract (ADR-0005).
		p := sku.GetSkuPartNumber()
		if p == nil || *p == "" {
			continue
		}
		product := *p
		if pre := sku.GetPrepaidUnits(); pre != nil {
			if enabled := pre.GetEnabled(); enabled != nil {
				out = append(out, license.SeatSample(license.MetricSeatsTotal, vendor, product, unit, instance, float64(*enabled)))
			}
		}
		if consumed := sku.GetConsumedUnits(); consumed != nil {
			out = append(out, license.SeatSample(license.MetricSeatsUsed, vendor, product, unit, instance, float64(*consumed)))
		}
	}
	return out
}
