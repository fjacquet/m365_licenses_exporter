package m365

import (
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/fjacquet/licenses_exporter/internal/config"
	"github.com/fjacquet/licenses_exporter/internal/license"
	msgraphsdk "github.com/microsoftgraph/msgraph-sdk-go"
)

// graphScopes requests the app-only default scope; the app registration must be
// granted Organization.Read.All (or Directory.Read.All) — see docs/deployment.
var graphScopes = []string{"https://graph.microsoft.com/.default"}

// NewSources builds one Source per configured tenant.
func NewSources(cfg config.M365Raw) ([]license.Source, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	var out []license.Source
	for _, t := range cfg.Tenants {
		secret, err := config.ResolveSecret(t.ClientSecret, t.ClientSecretFile)
		if err != nil {
			return nil, fmt.Errorf("m365 tenant %q: %w", t.Instance, err)
		}
		cred, err := azidentity.NewClientSecretCredential(t.TenantID, t.ClientID, secret, nil)
		if err != nil {
			return nil, fmt.Errorf("m365 tenant %q credential: %w", t.Instance, err)
		}
		client, err := msgraphsdk.NewGraphServiceClientWithCredentials(cred, graphScopes)
		if err != nil {
			return nil, fmt.Errorf("m365 tenant %q client: %w", t.Instance, err)
		}
		out = append(out, &source{instance: t.Instance, lister: graphSkuLister{client: client}})
	}
	return out, nil
}
