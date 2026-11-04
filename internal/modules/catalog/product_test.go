package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestProductCursorDeepPagesAreSignedAndKeysetStable(t *testing.T) {
	signer, err := NewProductCursorSigner([]byte("01234567890123456789012345678901"))
	require.NoError(t, err)
	rows := make([]ProductSummary, 212)
	for i := range rows {
		rows[i] = ProductSummary{ID: int64(12000 - i), Title: "item", UpdatedAt: time.Date(2026, 11, 4, 0, 0, i, 0, time.UTC)}
	}
	store := &cursorPageStore{rows: rows, size: 2}
	service, err := NewProductService(store, func(ctx context.Context, _, _ int64) (context.Context, error) { return ctx, nil }, signer)
	require.NoError(t, err)
	query := ProductListQuery{Page: 100, PageSize: 2, Sort: "-updated_at"}
	page100, err := service.List(context.Background(), 7, 9, query)
	require.NoError(t, err)
	require.Equal(t, 100, page100.Page)
	require.NotEmpty(t, page100.NextCursor)
	_, err = service.List(context.Background(), 7, 9, ProductListQuery{Page: 101, PageSize: 2, Sort: "-updated_at"})
	require.Error(t, err, "deep pages require the signed continuation from page 100")
	query.Cursor = page100.NextCursor
	seen := map[int64]bool{}
	for page := 101; page <= 105; page++ {
		result, err := service.List(context.Background(), 7, 9, query)
		require.NoError(t, err)
		require.Equal(t, page, result.Page)
		for _, row := range result.List {
			require.False(t, seen[row.ID], "duplicate product %d", row.ID)
			seen[row.ID] = true
		}
		require.NotEmpty(t, result.NextCursor)
		query.Cursor = result.NextCursor
	}
	require.Len(t, seen, 10)
	claims, err := signer.Parse(query.Cursor, 7, query)
	require.NoError(t, err)
	require.Equal(t, 106, claims.Page)
	_, err = signer.Parse(query.Cursor, 8, query)
	require.Error(t, err)
	query.Keyword = "tampered"
	_, err = signer.Parse(query.Cursor, 7, query)
	require.Error(t, err)
}

func TestProductListingViewsRemoveReservedMetadata(t *testing.T) {
	listing := SafeListingView(Listing{ID: 1, Ext: json.RawMessage(`{"channel":"web","_ingestion":{"raw_id":1}}`)})
	sku := SafeListingSKUView(ListingSKU{ID: 2, Ext: json.RawMessage(`{"_ingestion":{"raw_id":2}}`)})
	raw, err := json.Marshal(struct {
		Listing ListingView    `json:"listing"`
		SKU     ListingSKUView `json:"sku"`
	}{listing, sku})
	require.NoError(t, err)
	require.NotContains(t, string(raw), ReservedExtKey)
	require.NotContains(t, string(raw), "raw_id")
}

type cursorPageStore struct {
	rows []ProductSummary
	size int
}

func (s *cursorPageStore) Transaction(_ context.Context, fn func(context.Context) error) error {
	return fn(context.Background())
}
func (s *cursorPageStore) ListProducts(_ context.Context, _ int64, query ProductListQuery, position *ProductCursorPosition) ([]ProductSummary, int64, bool, error) {
	start := 0
	if position != nil {
		for i, row := range s.rows {
			if row.ID == position.ID {
				start = i + 1
				break
			}
		}
	} else if query.Page > 100 {
		start = (query.Page - 101) * s.size
	}
	end := start + query.PageSize
	if end > len(s.rows) {
		end = len(s.rows)
	}
	if position == nil && query.Page <= 100 {
		start = (query.Page - 1) * query.PageSize
		end = start + query.PageSize
		if end > len(s.rows) {
			end = len(s.rows)
		}
	}
	return s.rows[start:end], int64(len(s.rows)), end < len(s.rows), nil
}
func (*cursorPageStore) Product(context.Context, int64, int64) (ProductDetail, error) {
	return ProductDetail{}, errors.New("unused")
}
func (*cursorPageStore) ListListings(context.Context, int64, ProductListQuery) ([]ListingView, int64, error) {
	return nil, 0, errors.New("unused")
}
func (*cursorPageStore) CreateSPU(context.Context, *ProductSPU) error { return errors.New("unused") }
func (*cursorPageStore) UpdateSPU(context.Context, int64, int64, int, ProductUpdateRequest) error {
	return errors.New("unused")
}
func (*cursorPageStore) ArchiveSPU(context.Context, int64, int64, int) error {
	return errors.New("unused")
}
func (*cursorPageStore) ReplaceManualSKUs(context.Context, int64, int64, []ProductSKUInput) error {
	return errors.New("unused")
}
func (*cursorPageStore) MergeListings(context.Context, int64, int64, []int64) error {
	return errors.New("unused")
}
func (*cursorPageStore) UnmergeListing(context.Context, int64, int64, int64) error {
	return errors.New("unused")
}
func (*cursorPageStore) RecomputeSPUSummary(context.Context, int64, int64) error {
	return errors.New("unused")
}
