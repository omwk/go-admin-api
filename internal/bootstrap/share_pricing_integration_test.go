package bootstrap

import (
	"fmt"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/share"
	"github.com/omwk/go-admin-api/internal/modules/share/pricing"
	"github.com/stretchr/testify/require"
)

func TestPriceListIntegrationFourLayersTwoCustomersAndDisabledCustomer(t *testing.T) {
	// REQ-SHR-002/009: identical products must never inherit another customer's terms.
	f := newShareFixture(t)
	a, b := f.customer(t, "A"), f.customer(t, "B")
	quote, customer, defaults, other := f.list(t, &a.ID), f.list(t, &a.ID), f.list(t, nil), f.list(t, &b.ID)
	f.batch(t, &quote, share.ItemInput{SPUID: f.spu, PriceCents: 100, MOQ: 2})
	f.batch(t, &customer, share.ItemInput{SPUID: f.spu, SKUID: f.sku, PriceCents: 200})
	f.batch(t, &defaults, share.ItemInput{SPUID: f.spu, SKUID: f.sku, PriceCents: 300})
	f.batch(t, &other, share.ItemInput{SPUID: f.spu, SKUID: f.sku, PriceCents: 400})
	for _, tc := range []struct {
		who, selected int64
		basis         pricing.Basis
		price         int64
	}{
		{a.ID, quote.ID, pricing.CustomerQuote, 100}, {b.ID, other.ID, pricing.CustomerQuote, 400},
	} {
		out := f.preview(t, f.input(tc.who, tc.selected))
		require.Len(t, out, 1)
		require.Equal(t, tc.basis, out[0].PriceBasis)
		require.EqualValues(t, tc.price, *out[0].PriceCents)
		require.Equal(t, f.now, out[0].AsOf)
		require.NotEmpty(t, out[0].LayerName)
	}
	f.batch(t, &quote, share.ItemInput{SPUID: f.spu, SKUID: f.sku, PriceCents: 50})
	out := f.preview(t, f.input(a.ID, quote.ID))
	require.EqualValues(t, 50, *out[0].PriceCents)
	require.Equal(t, "sku", out[0].Precision)
	f.batch(t, &quote, share.ItemInput{SPUID: f.spu, Delete: true}, share.ItemInput{SPUID: f.spu, SKUID: f.sku, Delete: true})
	out = f.preview(t, f.input(a.ID, quote.ID))
	require.Equal(t, pricing.CustomerList, out[0].PriceBasis)
	f.batch(t, &customer, share.ItemInput{SPUID: f.spu, SKUID: f.sku, Delete: true})
	out = f.preview(t, f.input(a.ID, quote.ID))
	require.Equal(t, pricing.DefaultList, out[0].PriceBasis)
	f.batch(t, &defaults, share.ItemInput{SPUID: f.spu, SKUID: f.sku, Delete: true})
	out = f.preview(t, f.input(a.ID, quote.ID))
	require.Equal(t, pricing.Original, out[0].PriceBasis)
	require.EqualValues(t, 900, *out[0].PriceCents)
	r, _ := f.request(t, "PUT", fmt.Sprintf("/customers/%d", b.ID), share.CustomerInput{Code: b.Code, Name: b.Name, Status: "disabled", Version: b.Version})
	require.Equal(t, 200, r.Code, r.Body.String())
	out = f.preview(t, f.input(b.ID, other.ID))
	require.Equal(t, pricing.Original, out[0].PriceBasis)
	r, e := f.request(t, "POST", "/price-preview", f.input(a.ID, other.ID))
	require.Equal(t, 400, r.Code)
	require.Equal(t, 10001, e.Code)
}

func TestPriceListIntegrationConflictRollbackMOQAndMembership(t *testing.T) {
	f := newShareFixture(t)
	c := f.customer(t, "conflict")
	first, second := f.list(t, &c.ID), f.list(t, &c.ID)
	for _, list := range []*share.PriceList{&first, &second} {
		f.batch(t, list, share.ItemInput{SPUID: f.spu, SKUID: f.sku, PriceCents: 200, MOQ: 10})
	}
	in := f.input(c.ID, 0)
	r, e := f.request(t, "POST", "/price-preview", in)
	require.Equal(t, 409, r.Code)
	require.Equal(t, 10004, e.Code)
	require.Contains(t, r.Body.String(), "同层价格冲突")
	in.PriceListID = first.ID
	r, e = f.request(t, "POST", "/price-preview", in)
	require.Equal(t, 409, r.Code)
	require.Equal(t, 10004, e.Code)
	require.Contains(t, r.Body.String(), "起订量")
	path := fmt.Sprintf("/price-lists/%d/items:batch", first.ID)
	bad := share.BatchInput{Version: first.Version, Items: []share.ItemInput{{SPUID: f.spu, PriceCents: 1}, {SPUID: f.spu, SKUID: f.sku + 9999, PriceCents: 1}}}
	r, _ = f.request(t, "POST", path, bad)
	require.Equal(t, 404, r.Code)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var count int64
	require.NoError(t, q.Table("price_list_item").Where("price_list_id=? AND sku_id=0", first.ID).Count(&count).Error)
	require.Zero(t, count, "invalid member rolls back entire batch")
	bad.Items = []share.ItemInput{{SPUID: f.spu, PriceCents: -1}}
	r, _ = f.request(t, "POST", path, bad)
	require.Equal(t, 400, r.Code)
	bad.Items = []share.ItemInput{{SPUID: f.spu, PriceCents: 1}, {SPUID: f.spu, PriceCents: 2}}
	r, _ = f.request(t, "POST", path, bad)
	require.Equal(t, 400, r.Code)
	f.batch(t, &first, share.ItemInput{SPUID: f.spu, SKUID: f.sku, PriceCents: 123})
	bad.Items = []share.ItemInput{{SPUID: f.spu, PriceCents: 5}}
	r, e = f.request(t, "POST", path, bad)
	require.Equal(t, 409, r.Code)
	require.Equal(t, 10409, e.Code)
}

func TestPriceListIntegrationValidityCurrencyAndMissingOriginal(t *testing.T) {
	f := newShareFixture(t)
	c := f.customer(t, "validity")
	list := f.list(t, &c.ID)
	f.batch(t, &list, share.ItemInput{SPUID: f.spu, SKUID: f.sku, PriceCents: 100})
	end := f.now
	r, _ := f.request(t, "PUT", fmt.Sprintf("/price-lists/%d", list.ID), share.ListInput{Type: "customer", CustomerID: &c.ID, Name: list.Name, Currency: "CNY", Status: "active", Version: list.Version, ValidTo: &end})
	require.Equal(t, 200, r.Code, r.Body.String())
	out := f.preview(t, f.input(c.ID, list.ID))
	require.Equal(t, pricing.Original, out[0].PriceBasis)
	in := f.input(c.ID, 0)
	in.Currency = "USD"
	out = f.preview(t, in)
	require.Equal(t, pricing.Unpriced, out[0].PriceBasis)
	require.Nil(t, out[0].PriceCents)
	in = f.input(c.ID, 0)
	in.Items[0].SKUID = 0
	out = f.preview(t, in)
	require.False(t, out[0].ShowPrice)
	require.Nil(t, out[0].PriceCents, "SPU platform ranges are never original SKU prices")
}
