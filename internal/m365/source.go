package m365

import (
	"context"

	"github.com/fjacquet/licenses_exporter/internal/license"
	"github.com/microsoftgraph/msgraph-sdk-go/models"
)

// skuLister isolates the Graph SDK so the Source is unit-testable.
type skuLister interface {
	listSkus(ctx context.Context) ([]models.SubscribedSkuable, error)
}

type source struct {
	instance string
	lister   skuLister
}

func (s *source) Vendor() string   { return vendor }
func (s *source) Instance() string { return s.instance }

func (s *source) Collect(ctx context.Context) ([]license.Sample, error) {
	skus, err := s.lister.listSkus(ctx)
	if err != nil {
		return nil, err
	}
	return skusToSamples(s.instance, skus), nil
}
