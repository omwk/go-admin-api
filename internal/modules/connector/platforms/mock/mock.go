package mock

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/omwk/go-admin-api/internal/modules/connector"
)

//go:embed testdata/*.json
var fixtureFS embed.FS

// Platform serves normal, missing-field, multi-SKU and unknown-status fixtures.
type Platform struct {
	Fault           string
	dataset         []json.RawMessage
	categoryDataset []json.RawMessage
	orderDataset    []json.RawMessage
}

// NewCategoryDataset serves an isolated category tree through the normal mock pager.
func NewCategoryDataset(items []json.RawMessage) *Platform {
	p := &Platform{categoryDataset: make([]json.RawMessage, len(items))}
	for i, item := range items {
		p.categoryDataset[i] = append(json.RawMessage(nil), item...)
	}
	return p
}

// NewDataset serves an isolated, copied dataset through the normal mock pager.
func NewDataset(items []json.RawMessage) *Platform {
	p := &Platform{dataset: make([]json.RawMessage, len(items))}
	for i, item := range items {
		p.dataset[i] = append(json.RawMessage(nil), item...)
	}
	return p
}

// New creates a mock adapter. Fault may be overridden by MOCK_FAULT.
func New(fault string) *Platform {
	if value := os.Getenv("MOCK_FAULT"); value != "" {
		fault = value
	}
	return &Platform{Fault: fault}
}
func (p *Platform) Code() string { return "mock" }
func (p *Platform) Capabilities() connector.Capabilities {
	return connector.Capabilities{Resources: []connector.ResourceType{connector.ResourceProduct, connector.ResourceCategory, connector.ResourceOrder}, IncrementalModes: []string{"page", "timestamp"}}
}

// Fetch returns a bounded page from the fixture and supports cursor paging.
func (p *Platform) Fetch(_ context.Context, _ connector.Credential, request connector.FetchRequest) (connector.FetchResult, error) {
	if request.Resource != connector.ResourceProduct && request.Resource != connector.ResourceCategory {
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
	var err error
	payloads := p.dataset
	if request.Resource == connector.ResourceCategory {
		payloads = p.categoryDataset
		if payloads == nil {
			payloads, err = defaultCategories()
			if err != nil {
				return connector.FetchResult{}, err
			}
		}
	}
	if payloads == nil {
		raw, readErr := fixtureFS.ReadFile("testdata/" + name)
		if readErr != nil {
			return connector.FetchResult{}, readErr
		}
		if err := json.Unmarshal(raw, &payloads); err != nil {
			return connector.FetchResult{}, err
		}
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

func defaultCategories() ([]json.RawMessage, error) {
	items := make([]json.RawMessage, 0, 20)
	appendCategory := func(id int, parent string) error {
		row := map[string]string{"id": fmt.Sprintf("category-%02d", id), "name": fmt.Sprintf("Category %02d", id)}
		if parent != "" {
			row["parent_external_id"] = parent
		}
		data, err := json.Marshal(row)
		if err != nil {
			return err
		}
		items = append(items, data)
		return nil
	}
	for i := 1; i <= 2; i++ {
		if err := appendCategory(i, ""); err != nil {
			return nil, err
		}
	}
	for i := 3; i <= 8; i++ {
		if err := appendCategory(i, fmt.Sprintf("category-%02d", (i-3)/3+1)); err != nil {
			return nil, err
		}
	}
	for i := 9; i <= 20; i++ {
		if err := appendCategory(i, fmt.Sprintf("category-%02d", (i-9)/2+3)); err != nil {
			return nil, err
		}
	}
	return items, nil
}
