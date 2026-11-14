package bootstrap

import (
	"fmt"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/stretchr/testify/require"
)

func TestOrderQueryIntegrationCombinedFiltersPaginationVisibilityAndStatistics(t *testing.T) {
	// REQ-ORD-005/006/009: list visibility never changes historical monetary facts.
	f := newOrderQueryFixture(t)
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	first := f.add(t, "query-first", start)
	second := f.add(t, "query-second", start.Add(time.Hour))
	f.add(t, "query-outside", start.Add(2*time.Hour))
	path := fmt.Sprintf("/orders?shop_id=%d&status=pending_shipment&created_from=2026-11-01T00:00:00Z&created_to=2026-11-01T02:00:00Z&min_amount_cents=506&max_amount_cents=506&page_size=1", f.shop.ID)
	page := f.page(t, path)
	require.EqualValues(t, 2, page.Total)
	require.Len(t, page.List, 1)
	require.Equal(t, second, page.List[0].ID)
	next := f.page(t, path+"&page=2")
	require.Equal(t, first, next.List[0].ID)
	require.Equal(t, 2, next.Page)
	detail := f.detail(t, first)
	require.Len(t, detail.Items, 3)
	require.NotEmpty(t, detail.History)
	require.Positive(t, detail.Raw.RawID)
	before := f.stats(t)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.Table("sales_order").Where("id=?", first).Updates(map[string]any{"vanished_at": time.Now().UTC(), "vanish_reason": "platform_deleted"}).Error)
	page = f.page(t, "/orders")
	require.EqualValues(t, 2, page.Total)
	all := f.page(t, "/orders?include_vanished=true")
	require.EqualValues(t, 3, all.Total)
	detail = f.detail(t, first)
	require.NotNil(t, detail.VanishedAt)
	require.Equal(t, "platform_deleted", *detail.VanishReason)
	require.Equal(t, before, f.stats(t))
	require.EqualValues(t, 3, before[0].OrderCount)
	require.EqualValues(t, 1518, before[0].PaidAmountCents)
	require.NoError(t, q.Table("sales_order").Where("id=?", first).Updates(map[string]any{"vanished_at": nil, "vanish_reason": nil}).Error)
	require.EqualValues(t, 3, f.page(t, "/orders").Total)
}

func TestOrderQueryIntegrationPermissionDenialAndHiddenShop404(t *testing.T) {
	f := newOrderQueryFixture(t)
	id := f.add(t, "owned-query", time.Now().UTC())
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	ordinaryScopeActor(t, f.identityFixture, f.actor.UserID)
	require.NoError(t, q.Model(&model.Role{}).Where("tenant_id=1 AND code=?", identity.SuperAdminRoleCode).Update("data_scope", "self").Error)
	require.NoError(t, q.Table("shop").Where("id=?", f.shop.ID).Update("owner_user_id", f.user.ID).Error)
	response, envelope := f.request(t, "GET", fmt.Sprintf("/orders/%d", id), nil)
	require.Equal(t, 404, response.Code, response.Body.String())
	require.Equal(t, 50101, envelope.Code)
	require.Zero(t, f.page(t, "/orders?include_vanished=true").Total)
	require.Empty(t, f.stats(t))
	require.NoError(t, q.Table("shop").Where("id=?", f.shop.ID).Update("owner_user_id", f.actor.UserID).Error)
	require.NoError(t, q.Table("sales_order").Where("id=?", id).Update("tenant_id", 2).Error)
	response, envelope = f.request(t, "GET", fmt.Sprintf("/orders/%d", id), nil)
	require.Equal(t, 404, response.Code, response.Body.String())
	require.Equal(t, 50101, envelope.Code)
	require.Zero(t, f.page(t, "/orders?include_vanished=true").Total)
	assignProfileRole(t, f.identityFixture, identity.SalesRoleCode)
	f.bearer = f.login(t).AccessToken
	for _, path := range []string{"/orders", "/orders/stats", fmt.Sprintf("/orders/%d", id)} {
		response, envelope = f.request(t, "GET", path, nil)
		require.Equal(t, 403, response.Code, response.Body.String())
		require.Equal(t, 20107, envelope.Code)
	}
}

func TestOrderQueryIntegrationInvalidFiltersAndIndependentCurrencies(t *testing.T) {
	f := newOrderQueryFixture(t)
	f.add(t, "currency-cny", time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC))
	usd := f.add(t, "currency-usd", time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC))
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.Table("sales_order").Where("id=?", usd).Update("currency", "USD").Error)
	rows := f.stats(t)
	require.Len(t, rows, 2)
	require.Equal(t, "CNY", rows[0].Currency)
	require.Equal(t, "USD", rows[1].Currency)
	for _, path := range []string{"/orders?page=0", "/orders?page_size=101", "/orders?page=501&page_size=20", "/orders?status=invalid", "/orders?created_from=wrong", "/orders?created_from=2026-11-02T00:00:00Z&created_to=2026-11-01T00:00:00Z", "/orders?created_from=2026-11-01T00:00:00%2B08:00", "/orders?min_amount_cents=10&max_amount_cents=9", "/orders?min_amount_cents=1.2", "/orders?include_vanished=1", "/orders?shop_id=0", "/orders/invalid"} {
		response, envelope := f.request(t, "GET", path, nil)
		require.Equal(t, 400, response.Code, path)
		require.Equal(t, 10001, envelope.Code)
	}
	response, envelope := f.request(t, "GET", "/orders/999999", nil)
	require.Equal(t, 404, response.Code)
	require.Equal(t, 50101, envelope.Code)
}
