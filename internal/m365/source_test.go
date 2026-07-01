package m365

import (
	"context"
	"testing"

	"github.com/microsoftgraph/msgraph-sdk-go/models"
)

type fakeLister struct {
	skus []models.SubscribedSkuable
	err  error
}

func (f fakeLister) listSkus(context.Context) ([]models.SubscribedSkuable, error) {
	return f.skus, f.err
}

func TestSourceCollectUsesLister(t *testing.T) {
	sku := models.NewSubscribedSku()
	sku.SetSkuPartNumber(ptr("SPB"))
	sku.SetConsumedUnits(ptr(int32(5)))
	src := &source{instance: "tenant-a", lister: fakeLister{skus: []models.SubscribedSkuable{sku}}}

	samples, err := src.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range samples {
		if s.Name == "license_seats_used" && s.Value == 5 {
			found = true
		}
	}
	if !found {
		t.Fatal("expected seats_used=5 from lister SKUs")
	}
}
