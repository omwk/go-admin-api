package scope

import (
	"context"
	"errors"
)

type workAccessKey struct{}

// WithWorkAccess attaches a bootstrap-issued worker capability to a context.
func WithWorkAccess(ctx context.Context, access WorkAccess) context.Context {
	if ctx == nil {
		return nil
	}
	return context.WithValue(ctx, workAccessKey{}, access)
}

// WorkFrom returns a capability only when all required facts are present.
func WorkFrom(ctx context.Context) (WorkAccess, error) {
	if ctx == nil {
		return WorkAccess{}, errors.New("worker access denied")
	}
	access, ok := ctx.Value(workAccessKey{}).(WorkAccess)
	if !ok || access.tenantID <= 0 || access.jobID <= 0 || access.runID <= 0 || access.shopID <= 0 || access.mappingVersion <= 0 || access.generation <= 0 || !SupportedResource(access.resource) || len(access.methods) == 0 {
		return WorkAccess{}, errors.New("worker access denied")
	}
	return access, nil
}
