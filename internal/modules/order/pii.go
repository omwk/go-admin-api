package order

import (
	"context"
	"errors"
	"strconv"

	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/kernel/crypto"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
)

// PIIStore rechecks live shop scope and commits each disclosure's audit.
type PIIStore interface {
	Authorize(context.Context, int64, int64, string) (context.Context, error)
	Transaction(context.Context, func(context.Context) error) error
	Address(context.Context, int64, int64) (Address, error)
	Audit(context.Context, audit.Event) error
}

// RecentAuthentication verifies the current tenant/user/session's step-up window.
type RecentAuthentication func(context.Context, int64, int64, string) error

// PIIService exposes addresses only after authorization, authentication and durable audit.
type PIIService struct {
	store  PIIStore
	sealer crypto.Sealer
	recent RecentAuthentication
}

// NewPIIService requires authorization and authentication; missing keys fail closed on reads.
func NewPIIService(store PIIStore, sealer crypto.Sealer, recent RecentAuthentication) (*PIIService, error) {
	if store == nil || recent == nil {
		return nil, errors.New("order PII dependencies are required")
	}
	return &PIIService{store: store, sealer: sealer, recent: recent}, nil
}

// Read returns all three fields together after the successful audit transaction commits.
func (s *PIIService) Read(ctx context.Context, tenant, user, id int64, sid string) (AddressView, error) {
	if tenant <= 0 || user <= 0 || id <= 0 {
		return AddressView{}, invalidQuery()
	}
	authorized, err := s.store.Authorize(ctx, tenant, user, permission.TradeOrderPIIRead)
	if err == nil {
		err = s.recent(authorized, tenant, user, sid)
	}
	if err == nil && s.sealer == nil {
		err = errs.New(errs.ErrInternal, 503, "收货信息解密不可用")
	}
	var out AddressView
	if err == nil {
		err = s.store.Transaction(authorized, func(tx context.Context) error {
			row, err := s.store.Address(tx, tenant, id)
			if err != nil {
				return err
			}
			plain, err := OpenAddress(s.sealer, tenant, id, row)
			defer clearAddress(plain)
			if err != nil {
				return err
			}
			out = AddressView{ReceiverName: string(plain.ReceiverName), Phone: string(plain.Phone), Address: string(plain.Address)}
			return s.store.Audit(tx, piiAudit(tenant, user, id, "success"))
		})
	}
	if err != nil {
		if auditErr := s.store.Audit(ctx, piiAudit(tenant, user, id, "failure")); auditErr != nil {
			return AddressView{}, errs.New(errs.ErrInternal, 500, "收货信息审计失败")
		}
		return AddressView{}, err
	}
	return out, nil
}

func piiAudit(tenant, user, id int64, result string) audit.Event {
	return audit.Event{TenantID: tenant, ActorType: "user", ActorID: &user, Action: "order.pii.read", ResourceType: "order", ResourceID: strconv.FormatInt(id, 10),
		After: map[string]any{"fields": []string{"receiver_name", "phone", "address"}, "purpose": "order_detail"}, Result: result}
}
