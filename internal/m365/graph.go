package m365

import (
	"context"

	msgraphsdk "github.com/microsoftgraph/msgraph-sdk-go"
	msgraphcore "github.com/microsoftgraph/msgraph-sdk-go-core"
	"github.com/microsoftgraph/msgraph-sdk-go/models"
)

// graphSkuLister lists subscribedSkus via the Graph SDK, following @odata.nextLink.
type graphSkuLister struct {
	client *msgraphsdk.GraphServiceClient
}

func (g graphSkuLister) listSkus(ctx context.Context) ([]models.SubscribedSkuable, error) {
	page, err := g.client.SubscribedSkus().Get(ctx, nil)
	if err != nil {
		return nil, err
	}
	iterator, err := msgraphcore.NewPageIterator[models.SubscribedSkuable](
		page, g.client.GetAdapter(),
		models.CreateSubscribedSkuCollectionResponseFromDiscriminatorValue,
	)
	if err != nil {
		return nil, err
	}
	var out []models.SubscribedSkuable
	err = iterator.Iterate(ctx, func(item models.SubscribedSkuable) bool {
		if item != nil {
			out = append(out, item)
		}
		return true // keep paging
	})
	return out, err
}
