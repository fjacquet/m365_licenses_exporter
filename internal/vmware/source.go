package vmware

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/fjacquet/licenses_exporter/internal/license"
	"github.com/sirupsen/logrus"
	"github.com/vmware/govmomi"
	vlicense "github.com/vmware/govmomi/license"
	"github.com/vmware/govmomi/vim25/soap"
)

type source struct {
	instance string
	host     string
	username string
	password string
	insecure bool
}

func (s *source) Vendor() string   { return vendor }
func (s *source) Instance() string { return s.instance }

// Collect logs in fresh, lists licenses, and logs out — stateless per cycle
// (design spec §6). Logout uses a fresh background context (so it runs even if
// ctx was canceled mid-cycle) BOUNDED by a timeout so a stalled TCP can never
// block the deferred call indefinitely; a logout failure is logged so operators
// have visibility into potential vCenter session leaks.
func (s *source) Collect(ctx context.Context) ([]license.Sample, error) {
	u, err := soap.ParseURL(s.host)
	if err != nil {
		return nil, fmt.Errorf("parse vcenter url: %w", err)
	}
	u.User = url.UserPassword(s.username, s.password)

	c, err := govmomi.NewClient(ctx, u, s.insecure)
	if err != nil {
		return nil, fmt.Errorf("vcenter login: %w", err)
	}
	defer func() {
		logoutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := c.Logout(logoutCtx); err != nil {
			logrus.WithFields(logrus.Fields{"vendor": vendor, "instance": s.instance}).WithError(err).Warn("vcenter logout failed")
		}
	}()

	infos, err := vlicense.NewManager(c.Client).List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list licenses: %w", err)
	}
	return licensesToSamples(s.instance, infos), nil
}
