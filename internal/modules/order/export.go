package order

import (
	"context"
	"errors"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/idem"
	"github.com/omwk/go-admin-api/internal/modules/catalog"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
)

// ExportFilter lists approved non-PII predicates; arbitrary fields are never accepted.
type ExportFilter struct {
	ShopID          int64      `json:"shop_id,omitempty"`
	Status          string     `json:"status,omitempty"`
	CreatedFrom     *time.Time `json:"created_from,omitempty"`
	CreatedTo       *time.Time `json:"created_to,omitempty"`
	MinAmountCents  *int64     `json:"min_amount_cents,omitempty"`
	MaxAmountCents  *int64     `json:"max_amount_cents,omitempty"`
	IncludeVanished bool       `json:"include_vanished,omitempty"`
}

// Query produces the same filter contract as the default masked order list.
func (f ExportFilter) Query() Query {
	return Query{ShopID: f.ShopID, Status: f.Status, CreatedFrom: f.CreatedFrom, CreatedTo: f.CreatedTo,
		MinAmountCents: f.MinAmountCents, MaxAmountCents: f.MaxAmountCents, IncludeVanished: f.IncludeVanished}
}

// ExportRequest contains only the approved order filters.
type ExportRequest struct {
	Filters ExportFilter `json:"filters"`
}

// ExportSnapshot freezes filters, accessible shops and the field contract version.
type ExportSnapshot struct {
	ExportRequest
	FieldsVersion int     `json:"fields_version"`
	ShopIDs       []int64 `json:"shop_ids"`
}

// ExportStore persists jobs and rechecks the frozen shop scope at download time.
type ExportStore interface {
	ListOrderExports(context.Context, int64, int64, catalog.ExportListQuery) (catalog.ExportPage, error)
	CreateOrderExport(context.Context, int64, int64, ExportRequest, *idem.Operation) (catalog.ExportJob, error)
	CheckOrderExport(context.Context, int64, int64, int64) error
}

// ExportAuthorizer verifies the live order export permission and supplies human shop scope.
type ExportAuthorizer func(context.Context, int64, int64, string) (context.Context, error)

// ExportService exposes only PII-free asynchronous order exports.
type ExportService struct {
	store     ExportStore
	files     *catalog.ExportService
	authorize ExportAuthorizer
}

// NewExportService reuses frozen row publication and private object lifecycle management.
func NewExportService(store ExportStore, files *catalog.ExportService, authorize ExportAuthorizer) (*ExportService, error) {
	if store == nil || files == nil || authorize == nil {
		return nil, errors.New("order export dependencies required")
	}
	return &ExportService{store: store, files: files, authorize: authorize}, nil
}

// Create validates business filters before saving an immutable job request.
func (s *ExportService) Create(ctx context.Context, tenant, user int64, input ExportRequest, op *idem.Operation) (catalog.ExportJob, error) {
	ctx, err := s.authorize(ctx, tenant, user, permission.TradeOrderExport)
	if err != nil {
		return catalog.ExportJob{}, err
	}
	if _, err := normalizeQuery(input.Filters.Query()); err != nil {
		return catalog.ExportJob{}, err
	}
	for _, value := range []*time.Time{input.Filters.CreatedFrom, input.Filters.CreatedTo} {
		if value != nil {
			_, offset := value.Zone()
			if offset != 0 {
				return catalog.ExportJob{}, invalidQuery()
			}
		}
	}
	return s.store.CreateOrderExport(ctx, tenant, user, input, op)
}

// Status checks current permissions and every frozen shop before issuing a private URL.
func (s *ExportService) Status(ctx context.Context, tenant, user, id int64) (catalog.ExportStatus, error) {
	ctx, err := s.authorize(ctx, tenant, user, permission.TradeOrderExport)
	if err != nil {
		return catalog.ExportStatus{}, err
	}
	if err := s.store.CheckOrderExport(ctx, tenant, user, id); err != nil {
		return catalog.ExportStatus{}, err
	}
	return s.files.StatusKind(ctx, tenant, user, id, "orders")
}
