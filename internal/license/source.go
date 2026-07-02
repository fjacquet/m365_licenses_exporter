package license

import "context"

// Source collects license facts from a single configured target (one tenant or
// one vCenter). It returns samples already carrying vendor+instance labels; the
// collection loop stamps health/duration metrics around it.
type Source interface {
	Vendor() string
	Instance() string
	Collect(ctx context.Context) ([]Sample, error)
}
