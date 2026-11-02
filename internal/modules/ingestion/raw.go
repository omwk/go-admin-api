package ingestion

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ErrPartitionMissing is a recoverable infrastructure failure. Retain the page
// and checkpoint, repair coverage and retry; never advance the cursor on it.
var ErrPartitionMissing = errors.New("raw partition missing; retain checkpoint and retry")

// RawItem contains a complete response record and its collection metadata.
type RawItem struct {
	ExternalID  string
	Payload     json.RawMessage
	FetchedAt   time.Time
	APIName     string
	RequestMeta json.RawMessage
	HTTPStatus  *int
}

// RawRef identifies the original committed raw, including its partition key.
// IsNew describes this call; a duplicate returns the original ID and FetchedAt.
type RawRef struct {
	ID         int64
	ExternalID string
	FetchedAt  time.Time
	IsNew      bool
}

// RawIdentity is resolved from trusted access, never from a response payload.
type RawIdentity struct{ TenantID, PlatformID int64 }

// RawResolver verifies access to a shop and returns its tenant and platform.
// Worker capability validation will be supplied by the orchestration batch.
type RawResolver func(context.Context, int64) (RawIdentity, error)

// RawRecord is the validated persistence projection, scoped to one shop.
type RawRecord struct {
	RawItem
	PayloadSHA256 string
	PayloadSize   int
}

// RawStore persists a bounded batch on the service's active transaction.
type RawStore interface {
	Transaction(context.Context, func(context.Context) error) error
	SaveBatch(context.Context, RawIdentity, int64, string, []RawRecord) ([]RawRef, error)
}

// Service owns raw validation and short transaction boundaries.
type Service struct {
	store   RawStore
	resolve RawResolver
}

// NewService requires persistence and a trusted shop-access resolver.
func NewService(store RawStore, resolve RawResolver) (*Service, error) {
	if store == nil || resolve == nil {
		return nil, errors.New("raw store and shop resolver are required")
	}
	return &Service{store: store, resolve: resolve}, nil
}

// SaveRaw saves complete raw records in transactions of at most 500 items.
// Earlier committed batches survive a later failure; retrying them is safe.
// It has no cursor persistence dependency and cannot advance a checkpoint.
func (s *Service) SaveRaw(ctx context.Context, shopID int64, resource string, items []RawItem) ([]RawRef, error) {
	if shopID <= 0 || !validResource(resource) {
		return nil, errors.New("invalid raw shop or resource")
	}
	identity, err := s.resolve(ctx, shopID)
	if err != nil {
		return nil, err
	}
	if identity.TenantID <= 0 || identity.PlatformID <= 0 {
		return nil, errors.New("invalid raw identity")
	}
	result := make([]RawRef, 0, len(items))
	fetchedAt := time.Now().UTC()
	for start := 0; start < len(items); start += 500 {
		end := min(start+500, len(items))
		records, err := prepareRaw(items[start:end], fetchedAt)
		if err != nil {
			return nil, err
		}
		var refs []RawRef
		err = s.store.Transaction(ctx, func(tx context.Context) error {
			var saveErr error
			refs, saveErr = s.store.SaveBatch(tx, identity, shopID, resource, records)
			return saveErr
		})
		if err != nil {
			return nil, fmt.Errorf("save raw batch: %w", err)
		}
		result = append(result, refs...)
	}
	return result, nil
}

func prepareRaw(items []RawItem, fetchedAt time.Time) ([]RawRecord, error) {
	records := make([]RawRecord, 0, len(items))
	for _, item := range items {
		if item.ExternalID == "" {
			return nil, errors.New("raw external id is required")
		}
		hash, err := PayloadHash(item.Payload)
		if err != nil {
			return nil, err
		}
		if len(item.RequestMeta) == 0 {
			item.RequestMeta = json.RawMessage(`{}`)
		}
		if !json.Valid(item.RequestMeta) {
			return nil, errors.New("invalid raw request metadata")
		}
		if item.FetchedAt.IsZero() {
			item.FetchedAt = fetchedAt
		}
		item.FetchedAt = item.FetchedAt.UTC().Truncate(time.Microsecond)
		records = append(records, RawRecord{RawItem: item, PayloadSHA256: hash, PayloadSize: len(item.Payload)})
	}
	return records, nil
}

func validResource(resource string) bool {
	switch resource {
	case "product", "order", "inventory", "category":
		return true
	}
	return false
}
