package gateway

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/omwk/go-admin-api/internal/kernel/audit"
)

// ResourceGrant is the explicit application ceiling for one order.
type ResourceGrant struct {
	ID           int64      `json:"id"`
	TenantID     int64      `json:"-"`
	ClientID     int64      `json:"client_id"`
	ResourceType string     `json:"resource_type"`
	ResourceID   int64      `json:"resource_id"`
	PublicID     string     `json:"public_id"`
	Status       string     `json:"status"`
	Version      int        `json:"version"`
	GrantedBy    int64      `json:"-"`
	GrantedAt    time.Time  `json:"granted_at"`
	ExpiresAt    *time.Time `json:"expires_at"`
}

// ResourceStore persists grants after current owner and order-scope checks.
type ResourceStore interface {
	Transaction(context.Context, func(context.Context) error) error
	GrantOrder(context.Context, Actor, *ResourceGrant) error
	RevokeOrder(context.Context, Actor, int64, int64, int) error
	Audit(context.Context, audit.Event) error
}

// ResourceService owns the grant transaction and safe audit boundary.
type ResourceService struct{ store ResourceStore }

// NewResourceService requires primary-state resource persistence.
func NewResourceService(store ResourceStore) (*ResourceService, error) {
	if store == nil {
		return nil, errors.New("gateway resource store required")
	}
	return &ResourceService{store: store}, nil
}

// Grant publishes a new opaque identifier only for an explicitly selected order.
func (s *ResourceService) Grant(ctx context.Context, a Actor, clientID, orderID int64) (ResourceGrant, error) {
	if a.TenantID <= 0 || a.UserID <= 0 || clientID <= 0 || orderID <= 0 {
		return ResourceGrant{}, Invalid()
	}
	out := ResourceGrant{TenantID: a.TenantID, ClientID: clientID, ResourceType: "order", ResourceID: orderID, PublicID: ulid.Make().String(), Status: "active", Version: 1, GrantedBy: a.UserID, GrantedAt: time.Now().UTC()}
	err := s.store.Transaction(ctx, func(tx context.Context) error {
		if err := s.store.GrantOrder(tx, a, &out); err != nil {
			return err
		}
		return s.record(tx, a, clientID, "gateway.resource.grant", out.ID)
	})
	return out, err
}

// Revoke immediately closes the primary resource gate with optimistic locking.
func (s *ResourceService) Revoke(ctx context.Context, a Actor, clientID, grantID int64, version int) error {
	if a.TenantID <= 0 || a.UserID <= 0 || clientID <= 0 || grantID <= 0 || version <= 0 {
		return Invalid()
	}
	return s.store.Transaction(ctx, func(tx context.Context) error {
		if err := s.store.RevokeOrder(tx, a, clientID, grantID, version); err != nil {
			return err
		}
		return s.record(tx, a, clientID, "gateway.resource.revoke", grantID)
	})
}

func (s *ResourceService) record(ctx context.Context, a Actor, clientID int64, action string, id int64) error {
	return s.store.Audit(ctx, audit.Event{TenantID: a.TenantID, ActorType: "user", ActorID: &a.UserID, Action: action, ResourceType: "api_client", ResourceID: strconv.FormatInt(clientID, 10), After: map[string]any{"grant_id": id}, Result: "success"})
}
