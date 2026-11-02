package mock

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"os"
	"strconv"

	"github.com/omwk/go-admin-api/internal/modules/connector"
)

//go:embed testdata/*.json
var fixtureFS embed.FS

// Platform serves normal, missing-field, multi-SKU and unknown-status fixtures.
type Platform struct{ Fault string }

// New creates a mock adapter. Fault may be overridden by MOCK_FAULT.
func New(fault string) *Platform {
	if value := os.Getenv("MOCK_FAULT"); value != "" {
		fault = value
	}
	return &Platform{Fault: fault}
}
func (p *Platform) Code() string { return "mock" }
func (p *Platform) Capabilities() connector.Capabilities {
	return connector.Capabilities{Resources: []connector.ResourceType{connector.ResourceProduct}, IncrementalModes: []string{"page"}}
}

// Fetch returns a bounded page from the fixture and supports cursor paging.
func (p *Platform) Fetch(_ context.Context, _ connector.Credential, request connector.FetchRequest) (connector.FetchResult, error) {
	if request.Resource != connector.ResourceProduct {
		return connector.FetchResult{}, errors.New("mock resource is unsupported")
	}
	name := "normal.json"
	switch p.Fault {
	case "auth_expired":
		return connector.FetchResult{}, &connector.PlatformError{Kind: connector.ErrAuthExpired, Status: 401}
	case "rate_limited":
		return connector.FetchResult{}, &connector.PlatformError{Kind: connector.ErrRateLimited, Status: 429, RetryAfterSeconds: 60}
	case "missing_field":
		name = "missing_field.json"
	case "unknown_status":
		name = "unknown_status.json"
	case "multi_sku":
		name = "multi_sku.json"
	case "500":
		return connector.FetchResult{}, &connector.PlatformError{Kind: connector.ErrUnavailable, Status: 500}
	}
	raw, err := fixtureFS.ReadFile("testdata/" + name)
	if err != nil {
		return connector.FetchResult{}, err
	}
	var payloads []json.RawMessage
	if err := json.Unmarshal(raw, &payloads); err != nil {
		return connector.FetchResult{}, err
	}
	size := request.PageSize
	if size <= 0 {
		size = 20
	}
	start := 0
	if request.Cursor != "" {
		start, err = strconv.Atoi(request.Cursor)
		if err != nil || start < 0 {
			return connector.FetchResult{}, &connector.PlatformError{Kind: connector.ErrInvalidRequest, Status: 400}
		}
	}
	if start >= len(payloads) {
		return connector.FetchResult{}, nil
	}
	end := start + size
	if end > len(payloads) {
		end = len(payloads)
	}
	items := make([]connector.FetchItem, 0, end-start)
	for _, item := range payloads[start:end] {
		var object map[string]any
		if json.Unmarshal(item, &object) != nil {
			continue
		}
		id, _ := object["id"].(string)
		if id == "" {
			id, _ = object["product_id"].(string)
		}
		if p.Fault == "drift" {
			object["drift_field"] = "fixture"
			item, err = json.Marshal(object)
			if err != nil {
				return connector.FetchResult{}, err
			}
		}
		items = append(items, connector.FetchItem{ExternalID: id, Payload: append(json.RawMessage(nil), item...)})
	}
	return connector.FetchResult{Items: items, NextCursor: strconv.Itoa(end), HasMore: end < len(payloads)}, nil
}
