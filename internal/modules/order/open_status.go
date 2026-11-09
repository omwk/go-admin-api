package order

import (
	"context"
	"errors"
	"time"

	"github.com/oklog/ulid/v2"
)

// ApplicationStatusRequest is a purpose-specific application read identity.
type ApplicationStatusRequest struct {
	TenantID, ClientID, OwnerUserID, SecretID int64
	AppID, PublicID                           string
}

// ApplicationStatus contains only facts approved for the open status contract.
type ApplicationStatus struct {
	ID, OrderNo, Status string
	UpdatedAt           time.Time
}

// ApplicationStatusStore intersects current owner scope with explicit order grants.
type ApplicationStatusStore interface {
	ReadApplicationStatus(context.Context, ApplicationStatusRequest) (ApplicationStatus, error)
}

// ApplicationStatusService never uses a human order query or session identity.
type ApplicationStatusService struct{ store ApplicationStatusStore }

// NewApplicationStatusService requires an application-specific persistence port.
func NewApplicationStatusService(store ApplicationStatusStore) (*ApplicationStatusService, error) {
	if store == nil {
		return nil, errors.New("application order status store required")
	}
	return &ApplicationStatusService{store: store}, nil
}

// Get validates the application identity and rechecks disclosure authorization.
func (s *ApplicationStatusService) Get(ctx context.Context, in ApplicationStatusRequest) (ApplicationStatus, error) {
	if in.TenantID <= 0 || in.ClientID <= 0 || in.OwnerUserID <= 0 || in.SecretID <= 0 {
		return ApplicationStatus{}, NotFound()
	}
	if _, err := ulid.ParseStrict(in.PublicID); err != nil {
		return ApplicationStatus{}, NotFound()
	}
	if _, err := s.store.ReadApplicationStatus(ctx, in); err != nil {
		return ApplicationStatus{}, err
	}
	return s.store.ReadApplicationStatus(ctx, in)
}
