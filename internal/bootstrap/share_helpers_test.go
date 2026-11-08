package bootstrap

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/catalog"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/share"
	"github.com/stretchr/testify/require"
)

type shareFixture struct {
	*credentialFixture
	pricing  *share.Service
	spu, sku int64
}

func newShareFixture(t *testing.T) *shareFixture {
	t.Helper()
	f := &shareFixture{credentialFixture: newCredentialFixture(t)}
	var err error
	f.pricing, err = share.NewService(share.Dependencies{Store: NewShareStore(f.database), Idempotency: newIdempotencyStore(f.database), Sealer: f.sealer,
		Now: func() time.Time { return f.now }})
	require.NoError(t, err)
	profiles, err := identity.NewProfileService(newProfileStore(f.database))
	require.NoError(t, err)
	require.NoError(t, share.RegisterRoutes(f.router, f.identityFixture.service, profiles, f.pricing))
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	spu := catalog.ProductSPU{TenantID: 1, Title: "Pricing product", Status: "active", Source: "manual", Attributes: []byte(`{}`), Ext: []byte(`{}`)}
	require.NoError(t, q.Create(&spu).Error)
	sku := catalog.ProductSKU{TenantID: 1, SPUID: spu.ID, Title: "Pricing SKU", Currency: "CNY", PriceCents: 900, Status: "active", Attributes: []byte(`{}`)}
	require.NoError(t, q.Create(&sku).Error)
	f.spu, f.sku = spu.ID, sku.ID
	return f
}

func (f *shareFixture) customer(t *testing.T, code string) share.CustomerView {
	t.Helper()
	r, e := f.request(t, "POST", "/customers", share.CustomerInput{Code: code, Name: "Company " + code, ContactName: "CONTACT-CANARY", Phone: "PHONE-CANARY", Email: "EMAIL-CANARY"})
	require.Equal(t, 201, r.Code, r.Body.String())
	var row share.CustomerView
	require.NoError(t, json.Unmarshal(e.Data, &row))
	return row
}

func (f *shareFixture) list(t *testing.T, customer *int64) share.PriceList {
	t.Helper()
	kind := "default"
	if customer != nil {
		kind = "customer"
	}
	r, e := f.request(t, "POST", "/price-lists", share.ListInput{Type: kind, CustomerID: customer, Name: "Price source", Currency: "CNY", Status: "active", Note: "LIST-NOTE-CANARY"})
	require.Equal(t, 201, r.Code, r.Body.String())
	var row share.PriceList
	require.NoError(t, json.Unmarshal(e.Data, &row))
	return row
}

func (f *shareFixture) batch(t *testing.T, list *share.PriceList, items ...share.ItemInput) {
	t.Helper()
	r, e := f.request(t, "POST", fmt.Sprintf("/price-lists/%d/items:batch", list.ID), share.BatchInput{Version: list.Version, Items: items})
	require.Equal(t, 200, r.Code, r.Body.String())
	var result share.BatchResult
	require.NoError(t, json.Unmarshal(e.Data, &result))
	require.Equal(t, len(items), result.Success)
	require.Empty(t, result.Failed)
	list.Version = result.Version
}

func (f *shareFixture) preview(t *testing.T, in share.PreviewInput) []share.PreviewResult {
	t.Helper()
	r, e := f.request(t, "POST", "/price-preview", in)
	require.Equal(t, 200, r.Code, r.Body.String())
	var out []share.PreviewResult
	require.NoError(t, json.Unmarshal(e.Data, &out))
	return out
}

func (f *shareFixture) input(customer, quote int64) share.PreviewInput {
	return share.PreviewInput{CustomerID: customer, PriceListID: quote, Currency: "CNY", AllowFallback: true,
		Items: []share.PreviewItem{{SPUID: f.spu, SKUID: f.sku, Quantity: 5}}}
}

func (f *shareFixture) keyed(t *testing.T, method, path, key string, payload any) (*httptest.ResponseRecorder, shopHTTPEnvelope) {
	t.Helper()
	body, err := json.Marshal(payload)
	require.NoError(t, err)
	req := httptest.NewRequest(method, "/api/v1/admin"+path, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+f.bearer)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", key)
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	var e shopHTTPEnvelope
	if w.Code != 204 {
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &e))
	}
	return w, e
}
