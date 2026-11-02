package ingestion

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type rawTestStore struct {
	batches [][]RawRecord
	err     error
	active  bool
}

func (s *rawTestStore) Transaction(ctx context.Context, fn func(context.Context) error) error {
	s.active = true
	defer func() { s.active = false }()
	return fn(ctx)
}

func (s *rawTestStore) SaveBatch(ctx context.Context, _ RawIdentity, _ int64, _ string, rows []RawRecord) ([]RawRef, error) {
	if !s.active {
		return nil, errors.New("missing transaction")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.batches = append(s.batches, rows)
	return nil, s.err
}

func testRawResolver(context.Context, int64) (RawIdentity, error) {
	return RawIdentity{TenantID: 1, PlatformID: 1}, nil
}

func TestSaveRawValidationAndAccess(t *testing.T) {
	for _, tc := range []struct {
		name     string
		shop     int64
		resource string
		item     RawItem
	}{
		{"shop", 0, "product", RawItem{}},
		{"resource", 1, "unknown", RawItem{}},
		{"external id", 1, "product", RawItem{Payload: json.RawMessage(`{}`)}},
		{"payload", 1, "product", RawItem{ExternalID: "a", Payload: json.RawMessage(`{`)}},
		{"metadata", 1, "product", RawItem{ExternalID: "a", Payload: json.RawMessage(`{}`), RequestMeta: json.RawMessage(`{`)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &rawTestStore{}
			service, err := NewService(store, testRawResolver)
			require.NoError(t, err)
			_, err = service.SaveRaw(context.Background(), tc.shop, tc.resource, []RawItem{tc.item})
			require.Error(t, err)
			require.Empty(t, store.batches)
		})
	}
	denied := errors.New("access denied")
	service, err := NewService(&rawTestStore{}, func(context.Context, int64) (RawIdentity, error) { return RawIdentity{}, denied })
	require.NoError(t, err)
	_, err = service.SaveRaw(context.Background(), 1, "product", nil)
	require.ErrorIs(t, err, denied)
	service, err = NewService(&rawTestStore{}, func(context.Context, int64) (RawIdentity, error) { return RawIdentity{}, nil })
	require.NoError(t, err)
	_, err = service.SaveRaw(context.Background(), 1, "product", nil)
	require.Error(t, err)
}

func TestSaveRawBatchMetadataAndFailure(t *testing.T) {
	store := &rawTestStore{}
	service, err := NewService(store, testRawResolver)
	require.NoError(t, err)
	items := make([]RawItem, 501)
	stamp := time.Date(2040, 2, 1, 8, 0, 0, 123456789, time.FixedZone("fixture", 8*60*60))
	for i := range items {
		items[i] = RawItem{ExternalID: "a", Payload: json.RawMessage(`{}`), FetchedAt: stamp}
	}
	items[500].FetchedAt = time.Time{}
	_, err = service.SaveRaw(context.Background(), 1, "product", items)
	require.NoError(t, err)
	require.Len(t, store.batches, 2)
	require.Len(t, store.batches[0], 500)
	require.Len(t, store.batches[1], 1)
	row := store.batches[0][0]
	require.Equal(t, time.UTC, row.FetchedAt.Location())
	require.Equal(t, 123456000, row.FetchedAt.Nanosecond())
	require.JSONEq(t, `{}`, string(row.RequestMeta))
	require.Equal(t, 2, row.PayloadSize)
	require.WithinDuration(t, time.Now(), store.batches[1][0].FetchedAt, time.Second)
	store.err = ErrPartitionMissing
	refs, err := service.SaveRaw(context.Background(), 1, "product", items[:1])
	require.Nil(t, refs)
	require.ErrorIs(t, err, ErrPartitionMissing)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = service.SaveRaw(ctx, 1, "product", items[:1])
	require.ErrorIs(t, err, context.Canceled)
}
