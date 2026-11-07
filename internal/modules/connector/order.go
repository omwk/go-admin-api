package connector

import (
	"context"
	"time"
)

// OrderCapabilities is a fail-closed declaration for two-stage order acquisition.
// PIIFree must remain false for real PII until protected raw storage is installed.
type OrderCapabilities struct {
	StableIdentity     bool
	ComparableRevision bool
	PIIFree            bool
}

// OrderListRequest freezes an inclusive/exclusive time window across all pages.
type OrderListRequest struct {
	From, Until time.Time
	Cursor      string
	PageSize    int
}

// OrderSummary contains only immutable identity and platform ordering evidence.
type OrderSummary struct {
	ExternalID string
	UpdatedAt  time.Time
	Revision   *int64
}

// OrderListResult carries an opaque stable page token, including equal timestamps.
type OrderListResult struct {
	Orders     []OrderSummary
	NextCursor string
	HasMore    bool
}

// OrderPlatform separates list discovery from authoritative details. An explicit
// deletion is returned as a raw response interpreted by the frozen mapping;
// a transport 404 by itself is never deletion evidence.
type OrderPlatform interface {
	OrderCapabilities() OrderCapabilities
	ListOrders(context.Context, Credential, OrderListRequest) (OrderListResult, error)
	FetchOrder(context.Context, Credential, string) (FetchItem, error)
}
