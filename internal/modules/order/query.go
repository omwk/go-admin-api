package order

import (
	"context"
	"errors"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
)

// Query contains only the supported non-PII order predicates.
type Query struct {
	Page, PageSize                 int
	ShopID                         int64
	Status                         string
	CreatedFrom, CreatedTo         *time.Time
	MinAmountCents, MaxAmountCents *int64
	IncludeVanished                bool
}

// QueryStore applies live human shop scope to every order read.
type QueryStore interface {
	Authorize(context.Context, int64, int64, string) (context.Context, error)
	Snapshot(context.Context, func(context.Context) error) error
	List(context.Context, int64, Query) (Page, error)
	Get(context.Context, int64, int64) (Detail, error)
	Stats(context.Context, int64, Query) ([]Stat, error)
}

// QueryService authorizes and validates the read-only order APIs.
type QueryService struct{ store QueryStore }

// NewQueryService requires a scoped persistence adapter.
func NewQueryService(store QueryStore) (*QueryService, error) {
	if store == nil {
		return nil, errors.New("order query store is required")
	}
	return &QueryService{store: store}, nil
}

// List returns one consistent, scoped page and count.
func (s *QueryService) List(ctx context.Context, tenant, user int64, query Query) (Page, error) {
	query, err := normalizeQuery(query)
	if err != nil {
		return Page{}, err
	}
	ctx, err = s.store.Authorize(ctx, tenant, user, permission.TradeOrderList)
	if err != nil {
		return Page{}, err
	}
	var out Page
	err = s.store.Snapshot(ctx, func(ctx context.Context) error {
		var err error
		out, err = s.store.List(ctx, tenant, query)
		return err
	})
	return out, err
}

// Get returns one aggregate from a single read snapshot, without address access.
func (s *QueryService) Get(ctx context.Context, tenant, user, id int64) (Detail, error) {
	if id <= 0 {
		return Detail{}, invalidQuery()
	}
	ctx, err := s.store.Authorize(ctx, tenant, user, permission.TradeOrderRead)
	if err != nil {
		return Detail{}, err
	}
	var out Detail
	err = s.store.Snapshot(ctx, func(ctx context.Context) error {
		var err error
		out, err = s.store.Get(ctx, tenant, id)
		return err
	})
	return out, err
}

// Stats retains vanished orders because their monetary facts remain historical.
func (s *QueryService) Stats(ctx context.Context, tenant, user int64, query Query) ([]Stat, error) {
	query, err := normalizeQuery(query)
	if err != nil {
		return nil, err
	}
	ctx, err = s.store.Authorize(ctx, tenant, user, permission.TradeOrderStats)
	if err != nil {
		return nil, err
	}
	return s.store.Stats(ctx, tenant, query)
}

func normalizeQuery(query Query) (Query, error) {
	if query.Page == 0 {
		query.Page = 1
	}
	if query.PageSize == 0 {
		query.PageSize = 20
	}
	if query.Page <= 0 || query.PageSize <= 0 || query.PageSize > 100 || query.Page > 10000 || (query.Page-1)*query.PageSize >= 10000 || query.ShopID < 0 {
		return Query{}, invalidQuery()
	}
	if query.Status != "" && !validDisplayStatus(query.Status) {
		return Query{}, invalidQuery()
	}
	if query.CreatedFrom != nil && query.CreatedTo != nil && !query.CreatedFrom.Before(*query.CreatedTo) {
		return Query{}, invalidQuery()
	}
	if query.MinAmountCents != nil && *query.MinAmountCents < 0 || query.MaxAmountCents != nil && *query.MaxAmountCents < 0 {
		return Query{}, invalidQuery()
	}
	if query.MinAmountCents != nil && query.MaxAmountCents != nil && *query.MinAmountCents > *query.MaxAmountCents {
		return Query{}, invalidQuery()
	}
	return query, nil
}

func validDisplayStatus(status string) bool {
	switch status {
	case "pending_payment", "pending_shipment", "shipped", "completed", "closed", "refunding", "refunded", "unknown":
		return true
	}
	return false
}

func invalidQuery() error { return errs.Invalid(errors.New("invalid order query")) }

// NotFound deliberately conflates missing and inaccessible order identities.
func NotFound() error { return errs.New(50101, 404, "订单不存在") }
