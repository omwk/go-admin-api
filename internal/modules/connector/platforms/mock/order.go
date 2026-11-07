package mock

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/connector"
)

type orderFixture struct {
	ExternalID string          `json:"external_order_id"`
	UpdatedAt  time.Time       `json:"updated_at"`
	Revision   int64           `json:"revision"`
	Payload    json.RawMessage `json:"-"`
}

// NewOrderDataset copies a synthetic, non-PII dataset for the two-stage adapter.
func NewOrderDataset(items []json.RawMessage) *Platform {
	p := &Platform{orderDataset: make([]json.RawMessage, len(items))}
	for i, item := range items {
		p.orderDataset[i] = append(json.RawMessage(nil), item...)
	}
	return p
}

// OrderCapabilities declares the mock fixture's stable identity and revision.
func (*Platform) OrderCapabilities() connector.OrderCapabilities {
	return connector.OrderCapabilities{StableIdentity: true, ComparableRevision: true, PIIFree: true}
}

func (p *Platform) orderFixtures(ctx context.Context) ([]orderFixture, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for fault, kind := range map[string]connector.ErrKind{"auth_expired": connector.ErrAuthExpired, "rate_limited": connector.ErrRateLimited, "500": connector.ErrUnavailable} {
		if p.Fault == fault {
			return nil, &connector.PlatformError{Kind: kind, RetryAfterSeconds: 60}
		}
	}
	payloads := p.orderDataset
	if payloads == nil {
		raw, err := fixtureFS.ReadFile("testdata/order.json")
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &payloads); err != nil {
			return nil, err
		}
	}
	rows := make([]orderFixture, len(payloads))
	for i, payload := range payloads {
		if err := json.Unmarshal(payload, &rows[i]); err != nil {
			return nil, err
		}
		if rows[i].ExternalID == "" || rows[i].UpdatedAt.IsZero() || rows[i].Revision < 0 {
			return nil, &connector.PlatformError{Kind: connector.ErrInvalidRequest}
		}
		rows[i].Payload = payload
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].UpdatedAt.Equal(rows[j].UpdatedAt) {
			return rows[i].ExternalID < rows[j].ExternalID
		}
		return rows[i].UpdatedAt.Before(rows[j].UpdatedAt)
	})
	return rows, nil
}

// ListOrders projects identity/time only and retains equal timestamps across pages.
func (p *Platform) ListOrders(ctx context.Context, _ connector.Credential, request connector.OrderListRequest) (connector.OrderListResult, error) {
	rows, err := p.orderFixtures(ctx)
	if err != nil {
		return connector.OrderListResult{}, err
	}
	var summaries []connector.OrderSummary
	for _, row := range rows {
		if row.UpdatedAt.Before(request.From) || !row.UpdatedAt.Before(request.Until) {
			continue
		}
		revision := row.Revision
		summaries = append(summaries, connector.OrderSummary{ExternalID: row.ExternalID, UpdatedAt: row.UpdatedAt, Revision: &revision})
	}
	start := 0
	if request.Cursor != "" {
		start, err = strconv.Atoi(request.Cursor)
		if err != nil || start < 0 {
			return connector.OrderListResult{}, &connector.PlatformError{Kind: connector.ErrInvalidRequest}
		}
	}
	if start >= len(summaries) {
		return connector.OrderListResult{}, nil
	}
	end := min(start+max(1, request.PageSize), len(summaries))
	return connector.OrderListResult{Orders: summaries[start:end], NextCursor: strconv.Itoa(end), HasMore: end < len(summaries)}, nil
}

// FetchOrder returns the complete raw fixture; transport misses remain retryable.
func (p *Platform) FetchOrder(ctx context.Context, _ connector.Credential, id string) (connector.FetchItem, error) {
	rows, err := p.orderFixtures(ctx)
	if err != nil {
		return connector.FetchItem{}, err
	}
	for _, row := range rows {
		if row.ExternalID == id {
			return connector.FetchItem{ExternalID: id, Payload: append(json.RawMessage(nil), row.Payload...)}, nil
		}
	}
	return connector.FetchItem{}, &connector.PlatformError{Kind: connector.ErrInvalidRequest, Status: 404}
}
