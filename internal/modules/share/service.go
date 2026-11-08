package share

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/idem"
)

// Service coordinates live authorization, private fields and atomic mutations.
type Service struct {
	store       Store
	idempotency idem.Store
	sealer      FieldSealer
	now         func() time.Time
}

// NewService requires persistence and idempotency; missing keys fail closed on writes.
func NewService(d Dependencies) (*Service, error) {
	if d.Store == nil || d.Idempotency == nil {
		return nil, errors.New("pricing store dependencies required")
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Service{store: d.Store, idempotency: d.Idempotency, sealer: d.Sealer, now: d.Now}, nil
}

// Authorize refreshes permission and effective data scope on every operation.
func (s *Service) Authorize(ctx context.Context, a Actor, code string) (context.Context, error) {
	return s.store.Authorize(ctx, a, code)
}

// Mutation owns transaction and optional persisted-safe response replay.
func (s *Service) Mutation(ctx context.Context, a Actor, op *idem.Operation, check func(context.Context, idem.Result) error, fn func(context.Context) (idem.Result, error)) (idem.Result, error) {
	if op != nil && (op.TenantID != a.TenantID || op.UserID != a.UserID) {
		return idem.Result{}, Invalid()
	}
	var out idem.Result
	err := s.store.Transaction(ctx, func(tx context.Context) error {
		if op != nil {
			claim, err := s.idempotency.Claim(tx, *op)
			if err != nil {
				return err
			}
			if claim.Action == idem.Replay {
				if err := check(tx, claim.Result); err != nil {
					return err
				}
				out = claim.Result
				return nil
			}
		}
		var err error
		out, err = fn(tx)
		if err != nil {
			return err
		}
		if op != nil {
			out, err = s.idempotency.Complete(tx, *op, out)
		}
		return err
	})
	if errors.Is(err, idem.ErrMismatch) {
		err = errs.New(errs.ErrIdempotencyMismatch, 409, "幂等请求不一致")
	}
	if errors.Is(err, idem.ErrProcessing) {
		err = errs.New(errs.ErrConflict, 409, "请求处理中")
	}
	return out, err
}

func (s *Service) audit(ctx context.Context, a Actor, entity string, id int64, action string) error {
	return s.store.Audit(ctx, audit.Event{TenantID: a.TenantID, ActorType: "user", ActorID: &a.UserID, Action: "trade." + entity + "." + action,
		ResourceType: entity, ResourceID: strconv.FormatInt(id, 10), Result: "success"})
}

// Customers returns only visible customer rows and the identically scoped count.
func (s *Service) Customers(ctx context.Context, a Actor, q Query) (Page[CustomerView], error) {
	q, err := NormalizeQuery(q)
	if err != nil {
		return Page[CustomerView]{}, err
	}
	rows, total, err := s.store.Customers(ctx, a.TenantID, q)
	out := Page[CustomerView]{List: []CustomerView{}, Total: total, Page: q.Page, PageSize: q.PageSize}
	for _, row := range rows {
		out.List = append(out.List, customerView(row))
	}
	return out, err
}

// Customer returns one masked scoped customer.
func (s *Service) Customer(ctx context.Context, a Actor, id int64) (CustomerView, error) {
	row, err := s.store.Customer(ctx, a.TenantID, id, false)
	return customerView(row), err
}

// SaveCustomer creates or replaces contacts atomically with an optimistic version.
func (s *Service) SaveCustomer(ctx context.Context, a Actor, id int64, in CustomerInput) (CustomerView, error) {
	in, err := normalizeCustomer(in, a.UserID)
	if err != nil {
		return CustomerView{}, err
	}
	creating := id == 0
	if !creating && in.Version <= 0 || creating && in.Version != 0 {
		return CustomerView{}, Invalid()
	}
	var out Customer
	err = s.store.Transaction(ctx, func(tx context.Context) error {
		if id > 0 {
			old, err := s.store.Customer(tx, a.TenantID, id, true)
			if err != nil {
				return err
			}
			if old.Version != in.Version {
				return VersionConflict()
			}
		} else {
			var err error
			id, err = s.store.NextID(tx, "customer")
			if err != nil {
				return err
			}
		}
		if err := s.store.ValidateOwner(tx, a.TenantID, in.OwnerUserID); err != nil {
			return err
		}
		version := 1
		if in.Version > 0 {
			version = in.Version + 1
		}
		out = Customer{ID: id, TenantID: a.TenantID, Code: in.Code, Name: in.Name, Version: version, Status: in.Status, OwnerUserID: in.OwnerUserID, Source: in.Source}
		if err := s.sealContact(&out, in); err != nil {
			return err
		}
		if err := s.store.SaveCustomer(tx, out, creating); err != nil {
			return err
		}
		if err := s.audit(tx, a, "customer", id, "save"); err != nil {
			return err
		}
		var err error
		out, err = s.store.Customer(tx, a.TenantID, id, false)
		return err
	})
	return customerView(out), err
}

// DeleteCustomer soft-deletes one visible customer with its expected version.
func (s *Service) DeleteCustomer(ctx context.Context, a Actor, id int64, version int) error {
	return s.store.Transaction(ctx, func(tx context.Context) error {
		if err := s.store.DeleteCustomer(tx, a.TenantID, id, version); err != nil {
			return err
		}
		return s.audit(tx, a, "customer", id, "delete")
	})
}
