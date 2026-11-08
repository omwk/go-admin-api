package bootstrap

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/catalog"
	"github.com/omwk/go-admin-api/internal/modules/share"
	"github.com/stretchr/testify/require"
)

func TestShareIntegrationFrozenPricesMembershipAndDTOWhitelist(t *testing.T) {
	// REQ-SHR-004/006/009: only explicitly approved fields and frozen members are public.
	f := newShareAccessFixture(t)
	c := f.customer(t, "frozen")
	list := f.list(t, &c.ID)
	end := f.now.Add(time.Hour)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.Table("price_list").Where("id=?", list.ID).Update("valid_to", end).Error)
	f.batch(t, &list, share.ItemInput{SPUID: f.spu, PriceCents: 1234, MOQ: 20, Note: "PRIVATE-ITEM-CANARY"})
	require.NoError(t, q.Table("product_sku").Where("id=?", f.sku).Updates(map[string]any{"stock_cached": 123456, "cost_cents": 998877, "attributes": `{"private":"PRIVATE-ATTRIBUTE-CANARY"}`}).Error)
	require.NoError(t, q.Table("product_spu").Where("id=?", f.spu).Updates(map[string]any{"description": "PRIVATE-DESCRIPTION-CANARY", "ext": `{"private":"PRIVATE-EXT-CANARY"}`}).Error)
	in := f.publication()
	in.CustomerID, in.PriceListID, in.Password = c.ID, list.ID, f.randomValue(t)
	link := f.issue(t, in)
	w := f.anonymous(t, "POST", link.URL+"/verify", "192.0.2.1", map[string]string{"password": in.Password}, nil)
	require.Equal(t, 200, w.Code)
	w = f.anonymous(t, "GET", link.URL, "192.0.2.1", nil, shareCookie(t, w))
	first := sharePublicData(t, w)
	cookie := shareCookie(t, w)
	require.EqualValues(t, 1234, *first.Items[0].DisplayPriceCents)
	require.Equal(t, 20, first.Items[0].MOQ)
	require.Equal(t, "充足", first.Items[0].StockDisplay)
	require.Equal(t, end, *first.ValidUntil)
	require.Equal(t, "正式报价", first.PriceBasis)
	require.Len(t, first.ContentVersion, 64)
	f.batch(t, &list, share.ItemInput{SPUID: f.spu, PriceCents: 7777, MOQ: 99})
	require.NoError(t, q.Table("product_sku").Where("id=?", f.sku).Updates(map[string]any{"price_cents": 8888, "stock_cached": 0, "title": "Updated SKU"}).Error)
	extra := catalog.ProductSKU{TenantID: 1, SPUID: f.spu, Title: "Unapproved variant", Currency: "CNY", PriceCents: 3333, Status: "active", Attributes: []byte(`{}`)}
	require.NoError(t, q.Create(&extra).Error)
	for _, path := range []string{link.URL, fmt.Sprintf("%s/items/%d", link.URL, f.spu)} {
		w = f.anonymous(t, "GET", path, "192.0.2.1", nil, cookie)
		got := sharePublicData(t, w)
		require.Equal(t, first, got)
		assertShareWhitelist(t, w.Body.Bytes())
		for _, forbidden := range []string{"tenant_id", "customer_id", "price_list_id", "original_price_cents", "stock_cached", "998877", "123456", "PRIVATE-", "Unapproved variant"} {
			require.NotContains(t, w.Body.String(), forbidden)
		}
	}
	f.now = end
	require.Equal(t, 404, f.anonymous(t, "GET", link.URL, "192.0.2.1", nil, cookie).Code)
}

func assertShareWhitelist(t *testing.T, raw []byte) {
	t.Helper()
	var envelope map[string]any
	require.NoError(t, json.Unmarshal(raw, &envelope))
	data := envelope["data"].(map[string]any)
	want := []string{"schema_version", "generated_at", "mode", "price_basis", "as_of", "content_version", "title", "valid_until", "currency", "require_password", "allow_inquiry", "items"}
	require.ElementsMatch(t, want, shareObjectKeys(data))
	for _, rawItem := range data["items"].([]any) {
		item := rawItem.(map[string]any)
		require.ElementsMatch(t, []string{"spu_id", "sku_id", "title", "sku_title", "display_price_cents", "show_price", "moq", "stock_display"}, shareObjectKeys(item))
		require.Contains(t, []string{"", "充足", "紧张", "缺货"}, item["stock_display"])
	}
}

func shareObjectKeys(value map[string]any) []string {
	out := make([]string, 0, len(value))
	for key := range value {
		out = append(out, key)
	}
	return out
}

func TestShareIntegrationPublicSelectedListAndSKUSubset(t *testing.T) {
	f := newShareAccessFixture(t)
	selected, other := f.list(t, nil), f.list(t, nil)
	f.batch(t, &selected, share.ItemInput{SPUID: f.spu, PriceCents: 1234, MOQ: 7})
	f.batch(t, &other, share.ItemInput{SPUID: f.spu, PriceCents: 5678, MOQ: 9})
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	extra := catalog.ProductSKU{TenantID: 1, SPUID: f.spu, Title: "Unselected SKU", Currency: "CNY", PriceCents: 4444, Status: "active", Attributes: []byte(`{}`)}
	require.NoError(t, q.Create(&extra).Error)
	in := f.publication()
	in.PriceListID = selected.ID
	in.Items[0].SKUIDs = []int64{f.sku}
	link := f.issue(t, in)
	w := f.anonymous(t, "GET", link.URL, "192.0.2.1", nil, nil)
	content := sharePublicData(t, w)
	require.Len(t, content.Items, 1)
	require.Equal(t, f.sku, content.Items[0].SKUID)
	require.EqualValues(t, 1234, *content.Items[0].DisplayPriceCents)
	require.Equal(t, 7, content.Items[0].MOQ)
	require.False(t, content.RequirePassword)
	require.NotContains(t, w.Body.String(), "Unselected SKU")
}
