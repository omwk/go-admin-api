package connector

import (
	"context"
	"encoding/json"
)

// ResourceType identifies a platform endpoint family.
type ResourceType string

const (
	ResourceProduct   ResourceType = "product"
	ResourceOrder     ResourceType = "order"
	ResourceInventory ResourceType = "inventory"
	ResourceCategory  ResourceType = "category"
)

// Capabilities describes resources and pagination modes supported by a platform.
type Capabilities struct {
	Resources        []ResourceType `json:"resources"`
	IncrementalModes []string       `json:"incremental_modes"`
}

// Credential is intentionally opaque to connectors; values are supplied by the credential port.
type Credential struct {
	Kind   string
	Values map[string]json.RawMessage
}

// FetchRequest is a bounded page request.
type FetchRequest struct {
	Resource ResourceType
	Cursor   string
	PageSize int
}

// FetchItem is a complete original platform record.
type FetchItem struct {
	ExternalID string
	Payload    json.RawMessage
}

// FetchResult is one page returned by a platform.
type FetchResult struct {
	Items      []FetchItem
	NextCursor string
	HasMore    bool
}

// Platform acquires records and normalizes transport errors.
type Platform interface {
	Code() string
	Capabilities() Capabilities
	Fetch(context.Context, Credential, FetchRequest) (FetchResult, error)
}
