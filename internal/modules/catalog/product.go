package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
)

// ProductService owns product validation, transactions and cursor policy.
type ProductService struct {
	store     ProductStore
	authorize ProductAuthorizer
	cursors   *ProductCursorSigner
}

// NewProductService constructs the product application service.
func NewProductService(store ProductStore, authorize ProductAuthorizer, cursors *ProductCursorSigner) (*ProductService, error) {
	if store == nil || authorize == nil || cursors == nil {
		return nil, errors.New("product service dependencies are required")
	}
	return &ProductService{store: store, authorize: authorize, cursors: cursors}, nil
}

// List returns one SPU page with signed keyset continuation for deep pages.
func (s *ProductService) List(ctx context.Context, tenant, user int64, query ProductListQuery) (ProductPage, error) {
	ctx, err := s.authorize(ctx, tenant, user)
	if err != nil {
		return ProductPage{}, err
	}
	query, err = normalizeProductQuery(query)
	if err != nil {
		return ProductPage{}, err
	}
	page := query.Page
	var position *ProductCursorPosition
	if query.Cursor != "" {
		claims, err := s.cursors.Parse(query.Cursor, tenant, query)
		if err != nil {
			return ProductPage{}, invalidProduct()
		}
		position = &claims.Position
		page = claims.Page
	} else if query.Page > 100 {
		return ProductPage{}, invalidProduct()
	}
	rows, total, more, err := s.store.ListProducts(ctx, tenant, query, position)
	if err != nil {
		return ProductPage{}, err
	}
	result := ProductPage{List: rows, Total: total, Page: page, PageSize: query.PageSize}
	if page >= 100 || query.Cursor != "" {
		if more && len(rows) > 0 {
			last := rows[len(rows)-1]
			result.NextCursor, err = s.cursors.Sign(tenant, query, ProductCursorPosition{UpdatedAt: last.UpdatedAt, ID: last.ID}, page+1)
			if err != nil {
				return ProductPage{}, err
			}
		}
	}
	return result, nil
}

// Get returns one tenant-owned SPU without raw payloads or internal SKU fields.
func (s *ProductService) Get(ctx context.Context, tenant, user, id int64) (ProductDetail, error) {
	ctx, err := s.authorize(ctx, tenant, user)
	if err != nil {
		return ProductDetail{}, err
	}
	if id <= 0 {
		return ProductDetail{}, invalidProduct()
	}
	return s.store.Product(ctx, tenant, id)
}

// ListingPage returns a listing-specific page without exposing raw metadata.
func (s *ProductService) ListingPage(ctx context.Context, tenant, user int64, query ProductListQuery) (ListingPage, error) {
	ctx, err := s.authorize(ctx, tenant, user)
	if err != nil {
		return ListingPage{}, err
	}
	query, err = normalizeProductQuery(query)
	if err != nil {
		return ListingPage{}, err
	}
	rows, total, err := s.store.ListListings(ctx, tenant, query)
	if err != nil {
		return ListingPage{}, err
	}
	return ListingPage{List: rows, Total: total, Page: query.Page, PageSize: query.PageSize}, nil
}

// ListingPage is the standard page envelope for safe listing projections.
type ListingPage struct {
	List     []ListingView `json:"list"`
	Total    int64         `json:"total"`
	Page     int           `json:"page"`
	PageSize int           `json:"page_size"`
}

// Create creates a manual SPU and all supplied SKUs atomically.
func (s *ProductService) Create(ctx context.Context, tenant, user int64, request ProductCreateRequest) (ProductDetail, error) {
	ctx, err := s.authorize(ctx, tenant, user)
	if err != nil {
		return ProductDetail{}, err
	}
	if err := validateProductCreate(request); err != nil {
		return ProductDetail{}, invalidProduct()
	}
	code := request.Code
	if code != nil {
		trimmed := strings.TrimSpace(*code)
		code = &trimmed
	}
	row := ProductSPU{TenantID: tenant, Code: code, Title: strings.TrimSpace(request.Title), Subtitle: request.Subtitle,
		Brand: request.Brand, CategoryID: request.CategoryID, MainImage: request.MainImage, Description: request.Description,
		Status: productStatus(request.Status), Source: "manual", Attributes: productJSON(request.Attributes), Ext: productJSON(request.Ext)}
	err = s.store.Transaction(ctx, func(tx context.Context) error {
		if err := s.store.CreateSPU(tx, &row); err != nil {
			return err
		}
		if err := s.store.ReplaceManualSKUs(tx, tenant, row.ID, request.SKUs); err != nil {
			return err
		}
		return s.store.RecomputeSPUSummary(tx, tenant, row.ID)
	})
	if err != nil {
		return ProductDetail{}, err
	}
	return s.store.Product(ctx, tenant, row.ID)
}

// Update replaces editable manual SPU fields with optimistic version checking.
func (s *ProductService) Update(ctx context.Context, tenant, user, id int64, request ProductUpdateRequest) (ProductDetail, error) {
	ctx, err := s.authorize(ctx, tenant, user)
	if err != nil {
		return ProductDetail{}, err
	}
	if id <= 0 || request.Version < 0 || strings.TrimSpace(request.Title) == "" || !validProductStatus(request.Status) ||
		!validProductJSON(request.Attributes) || !validProductJSON(request.Ext) ||
		(request.SKUs != nil && (len(*request.SKUs) > 500 || validateProductSKUs(*request.SKUs) != nil)) {
		return ProductDetail{}, invalidProduct()
	}
	err = s.store.Transaction(ctx, func(tx context.Context) error {
		if err := s.store.UpdateSPU(tx, tenant, id, request.Version, request); err != nil {
			return err
		}
		if request.SKUs != nil {
			if err := s.store.ReplaceManualSKUs(tx, tenant, id, *request.SKUs); err != nil {
				return err
			}
		}
		return s.store.RecomputeSPUSummary(tx, tenant, id)
	})
	if err != nil {
		return ProductDetail{}, err
	}
	return s.store.Product(ctx, tenant, id)
}

// Archive soft-deletes one SPU after checking its optimistic version.
func (s *ProductService) Archive(ctx context.Context, tenant, user, id int64, version int) error {
	ctx, err := s.authorize(ctx, tenant, user)
	if err != nil {
		return err
	}
	if id <= 0 || version < 0 {
		return invalidProduct()
	}
	return s.store.Transaction(ctx, func(tx context.Context) error { return s.store.ArchiveSPU(tx, tenant, id, version) })
}

// Merge attaches listings to an SPU and recomputes its denormalized columns atomically.
func (s *ProductService) Merge(ctx context.Context, tenant, user, spuID int64, listingIDs []int64) (ProductDetail, error) {
	ctx, err := s.authorize(ctx, tenant, user)
	if err != nil {
		return ProductDetail{}, err
	}
	if spuID <= 0 || len(listingIDs) == 0 || len(listingIDs) > 100 {
		return ProductDetail{}, invalidProduct()
	}
	seen := map[int64]bool{}
	for _, id := range listingIDs {
		if id <= 0 || seen[id] {
			return ProductDetail{}, invalidProduct()
		}
		seen[id] = true
	}
	err = s.store.Transaction(ctx, func(tx context.Context) error {
		if err := s.store.MergeListings(tx, tenant, spuID, listingIDs); err != nil {
			return err
		}
		return s.store.RecomputeSPUSummary(tx, tenant, spuID)
	})
	if err != nil {
		return ProductDetail{}, err
	}
	return s.store.Product(ctx, tenant, spuID)
}

// Unmerge detaches one listing and recomputes its former SPU in the same transaction.
func (s *ProductService) Unmerge(ctx context.Context, tenant, user, spuID, listingID int64) error {
	ctx, err := s.authorize(ctx, tenant, user)
	if err != nil {
		return err
	}
	if spuID <= 0 || listingID <= 0 {
		return invalidProduct()
	}
	return s.store.Transaction(ctx, func(tx context.Context) error {
		if err := s.store.UnmergeListing(tx, tenant, spuID, listingID); err != nil {
			return err
		}
		return s.store.RecomputeSPUSummary(tx, tenant, spuID)
	})
}

func normalizeProductQuery(query ProductListQuery) (ProductListQuery, error) {
	if query.Page == 0 {
		query.Page = 1
	}
	if query.PageSize == 0 {
		query.PageSize = 20
	}
	if query.Sort == "" {
		query.Sort = "-updated_at"
	}
	if query.Page < 1 || query.PageSize < 1 || query.PageSize > 100 || query.Page > 10000 || !validProductSort(query.Sort) ||
		(query.UpdatedFrom != nil && query.UpdatedTo != nil && !query.UpdatedFrom.Before(*query.UpdatedTo)) ||
		(query.PriceMinCents != nil && query.PriceMaxCents != nil && *query.PriceMinCents > *query.PriceMaxCents) {
		return ProductListQuery{}, invalidProduct()
	}
	return query, nil
}

func validProductSort(sort string) bool {
	switch sort {
	case "updated_at", "-updated_at", "title", "-title", "price_cents", "-price_cents", "price_min_cents", "-price_min_cents":
		return true
	default:
		return false
	}
}

func validProductStatus(status string) bool {
	return status == "draft" || status == "active" || status == "archived"
}

func productStatus(status string) string {
	if status == "" {
		return "active"
	}
	return status
}

func validateProductCreate(request ProductCreateRequest) error {
	if strings.TrimSpace(request.Title) == "" || !validProductStatus(productStatus(request.Status)) || len(request.SKUs) > 500 ||
		!validProductJSON(request.Attributes) || !validProductJSON(request.Ext) {
		return errors.New("invalid product")
	}
	return validateProductSKUs(request.SKUs)
}

func validateProductSKUs(skus []ProductSKUInput) error {
	seen := map[string]bool{}
	for _, sku := range skus {
		if strings.TrimSpace(sku.Title) == "" || sku.PriceCents < 0 || sku.CostCents < 0 || len(sku.Currency) != 3 ||
			(sku.Status != "" && sku.Status != "active" && sku.Status != "disabled") || !validProductJSON(sku.Attributes) {
			return errors.New("invalid product SKU")
		}
		if sku.Code != nil {
			code := strings.TrimSpace(*sku.Code)
			if code == "" || seen[code] {
				return errors.New("invalid product SKU code")
			}
			seen[code] = true
		}
	}
	return nil
}

func validProductJSON(raw json.RawMessage) bool { return len(raw) == 0 || json.Valid(raw) }

func productJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || !json.Valid(raw) || string(raw) == "null" {
		return json.RawMessage(`{}`)
	}
	return append(json.RawMessage(nil), raw...)
}

// SafeListingView strips ingestion-owned metadata from listing extension data.
func SafeListingView(row Listing) ListingView {
	safe := safeExt(row.Ext)
	return ListingView{ID: row.ID, ShopID: row.ShopID, PlatformID: row.PlatformID, SPUID: row.SPUID,
		ExternalProductID: row.ExternalProductID, Title: row.Title, PriceCents: row.PriceCents, Currency: row.Currency,
		Status: row.Status, StockTotal: row.StockTotal, SyncedAt: row.SyncedAt, Ext: safe}
}

// SafeListingSKUView strips ingestion-owned metadata from listing SKU extensions.
func SafeListingSKUView(row ListingSKU) ListingSKUView {
	return ListingSKUView{ID: row.ID, SKUID: row.SKUID, ExternalSKUID: row.ExternalSKUID, Title: row.Title,
		PriceCents: row.PriceCents, Stock: row.Stock, Ext: safeExt(row.Ext), SyncedAt: row.SyncedAt}
}

func safeExt(raw json.RawMessage) json.RawMessage {
	var ext map[string]json.RawMessage
	if json.Unmarshal(raw, &ext) != nil || ext == nil {
		return json.RawMessage(`{}`)
	}
	delete(ext, ReservedExtKey)
	safe, err := json.Marshal(ext)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return safe
}

func invalidProduct() error {
	return errs.New(errs.ErrInvalidParam, http.StatusBadRequest, "商品参数校验失败")
}
