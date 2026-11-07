package bootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/omwk/go-admin-api/internal/modules/connector"
	"github.com/omwk/go-admin-api/internal/modules/ingestion"
	"github.com/omwk/go-admin-api/internal/testenv"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

type orderTestPlatform struct {
	pages      []connector.OrderListResult
	details    map[string]json.RawMessage
	failures   map[string]bool
	calls      map[string]int
	requests   []connector.OrderListRequest
	beforeCall func()
	listError  bool
	pii        bool
	afterList  func()
}

func (*orderTestPlatform) Code() string { return "mock" }
func (*orderTestPlatform) Capabilities() connector.Capabilities {
	return connector.Capabilities{Resources: []connector.ResourceType{connector.ResourceOrder}, IncrementalModes: []string{"timestamp"}}
}
func (p *orderTestPlatform) OrderCapabilities() connector.OrderCapabilities {
	return connector.OrderCapabilities{StableIdentity: true, ComparableRevision: true, PIIFree: !p.pii}
}
func (*orderTestPlatform) Fetch(context.Context, connector.Credential, connector.FetchRequest) (connector.FetchResult, error) {
	return connector.FetchResult{}, &connector.PlatformError{Kind: connector.ErrInvalidRequest}
}
func (p *orderTestPlatform) ListOrders(_ context.Context, _ connector.Credential, r connector.OrderListRequest) (connector.OrderListResult, error) {
	if p.beforeCall != nil {
		p.beforeCall()
	}
	p.requests = append(p.requests, r)
	if p.listError {
		return connector.OrderListResult{}, &connector.PlatformError{Kind: connector.ErrUnavailable}
	}
	index := 0
	if r.Cursor != "" {
		if _, err := fmt.Sscanf(r.Cursor, "page-%d", &index); err != nil {
			return connector.OrderListResult{}, err
		}
	}
	if index >= len(p.pages) {
		return connector.OrderListResult{}, nil
	}
	if p.afterList != nil {
		p.afterList()
	}
	return p.pages[index], nil
}
func (p *orderTestPlatform) FetchOrder(_ context.Context, _ connector.Credential, id string) (connector.FetchItem, error) {
	if p.beforeCall != nil {
		p.beforeCall()
	}
	p.calls[id]++
	if p.failures[id] {
		return connector.FetchItem{}, &connector.PlatformError{Kind: connector.ErrUnavailable}
	}
	return connector.FetchItem{ExternalID: id, Payload: p.details[id]}, nil
}

func orderDetailPayload(t *testing.T, id string, revision int64) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"external_order_id": id, "order_no": "display", "lifecycle_status": "paid", "fulfillment_status": "unshipped", "payment_status": "paid", "after_sales_status": "none", "cancel_status": "none", "currency": "CNY",
		"goods_amount_cents": 100, "shipping_fee_cents": 0, "discount_cents": 0, "total_amount_cents": 100, "paid_amount_cents": 100, "refunded_amount_cents": 0, "payment_settled": true, "revision": revision,
		"items_complete": true, "items_has_more": false, "items_expected_count": 1, "allocation_source": "largest_remainder_v1", "items": []map[string]any{{"external_item_id": "line", "title": "sale snapshot", "currency": "CNY", "price_cents": 100, "qty": 1, "discount_cents": 0, "amount_cents": 100, "discount_eligible": true, "refund_status": "none"}}})
	require.NoError(t, err)
	return raw
}

func newOrderSyncFixture(t *testing.T, count int) (*syncFixture, *orderTestPlatform) {
	t.Helper()
	f := newSyncFixture(t, nil)
	p := &orderTestPlatform{details: map[string]json.RawMessage{}, failures: map[string]bool{}, calls: map[string]int{}}
	updated := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	revision := int64(1)
	page := connector.OrderListResult{}
	for i := 0; i < count; i++ {
		id := fmt.Sprintf("order-%02d", i)
		p.details[id] = orderDetailPayload(t, id, revision)
		page.Orders = append(page.Orders, connector.OrderSummary{ExternalID: id, UpdatedAt: updated, Revision: &revision})
	}
	p.pages = []connector.OrderListResult{page}
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	spec := ingestion.Spec{SchemaVersion: 1, Resource: "order", KeyField: "external_order_id", StatusTables: map[string]map[string]string{}}
	for _, key := range []string{"external_order_id", "order_no", "lifecycle_status", "fulfillment_status", "payment_status", "after_sales_status", "cancel_status", "platform_status", "currency", "goods_amount_cents", "shipping_fee_cents", "discount_cents", "total_amount_cents", "paid_amount_cents", "refunded_amount_cents", "payment_settled", "revision", "items_complete", "items_has_more", "items_expected_count", "allocation_source", "deleted", "vanish_reason"} {
		spec.Fields = append(spec.Fields, ingestion.FieldSpec{Target: key, Source: "$." + key})
	}
	collection := ingestion.CollectionSpec{Target: "items", Source: "$.items", KeyField: "external_item_id"}
	for _, key := range []string{"external_item_id", "title", "currency", "price_cents", "qty", "discount_cents", "amount_cents", "discount_eligible", "refund_status"} {
		collection.Fields = append(collection.Fields, ingestion.FieldSpec{Target: key, Source: "$." + key})
	}
	spec.Collections = []ingestion.CollectionSpec{collection}
	raw, err := json.Marshal(spec)
	require.NoError(t, err)
	compiled, err := ingestion.Compile(raw)
	require.NoError(t, err)
	require.NoError(t, q.Table("mapping_profile").Where("id IN (SELECT profile_id FROM mapping_version WHERE id=?)", f.mappingID).Update("resource_type", "order").Error)
	require.NoError(t, q.Table("mapping_version").Where("id=?", f.mappingID).Updates(map[string]any{"spec": json.RawMessage(raw), "checksum": compiled.Checksum}).Error)
	require.NoError(t, q.Table("sync_job").Where("id=?", f.jobID).Update("resource_type", "order").Error)
	require.NoError(t, q.Table("platform").Where("id=?", f.shop.PlatformID).Update("capabilities", json.RawMessage(`{"resources":["order"],"incremental_modes":["timestamp"]}`)).Error)
	registry := connector.NewRegistry()
	require.NoError(t, registry.Register(p))
	f.engine, f.engineStore, err = buildSyncRuntime(f.database, f.redis.cache, f.credentialFixture.service, registry, testenv.Timeouts(t).Sync, f.sealer)
	require.NoError(t, err)
	// Reset the shared bucket between fixture network calls; production still limits each call.
	p.beforeCall = func() {
		_, err := f.redis.cache.Del(f.ctx, fmt.Sprintf("sync:bucket:%d:%d", 1, f.shop.ID))
		require.NoError(t, err)
	}
	return f, p
}

func orderTableCount(t *testing.T, f *syncFixture, table, condition string) int64 {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var count int64
	require.NoError(t, q.Table(table).Where("tenant_id=1 AND shop_id=?", f.shop.ID).Where(condition).Count(&count).Error)
	return count
}
