package bootstrap

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/share"
	"github.com/stretchr/testify/require"
)

func TestPriceListIntegrationCRUDIdempotencyAndEncryptedNotes(t *testing.T) {
	f := newShareFixture(t)
	customer := f.customer(t, "notes")
	list := f.list(t, &customer.ID)
	input := share.BatchInput{Version: 1, Items: []share.ItemInput{{SPUID: f.spu, SKUID: f.sku, PriceCents: 123, Note: "ITEM-NOTE-CANARY"}}}
	path := fmt.Sprintf("/price-lists/%d", list.ID)
	r, e := f.keyed(t, "POST", path+"/items:batch", "batch-key", input)
	require.Equal(t, 200, r.Code, r.Body.String())
	require.Zero(t, e.Code)
	replayed, _ := f.keyed(t, "POST", path+"/items:batch", "batch-key", input)
	require.Equal(t, r.Body.String(), replayed.Body.String())
	input.Items[0].PriceCents = 124
	r, e = f.keyed(t, "POST", path+"/items:batch", "batch-key", input)
	require.Equal(t, 409, r.Code)
	require.Equal(t, 10006, e.Code)
	r, e = f.request(t, "GET", path, nil)
	require.Equal(t, 200, r.Code, r.Body.String())
	var detail share.ListDetail
	require.NoError(t, json.Unmarshal(e.Data, &detail))
	require.Equal(t, 2, detail.Version)
	require.Len(t, detail.Items, 1)
	for _, field := range []string{"cipher", "nonce", "key_version", "ITEM-NOTE-CANARY", "LIST-NOTE-CANARY"} {
		require.NotContains(t, r.Body.String(), field)
	}
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var stored share.Item
	require.NoError(t, q.Where("id=?", detail.Items[0].ID).Take(&stored).Error)
	aad, err := share.FieldAAD(1, "price_list_item", stored.ID, "note", stored.Revision)
	require.NoError(t, err)
	plain, err := f.sealer.Open(stored.Note.Cipher, stored.Note.Nonce, aad, stored.KeyVersion)
	require.NoError(t, err)
	require.Equal(t, "ITEM-NOTE-CANARY", string(plain))
	clear(plain)
	var snapshots []string
	require.NoError(t, q.Raw(`SELECT row_to_json(p)::text FROM price_list p UNION ALL SELECT row_to_json(i)::text FROM price_list_item i UNION ALL SELECT row_to_json(a)::text FROM audit_log a UNION ALL SELECT row_to_json(k)::text FROM idempotency_key k`).Scan(&snapshots).Error)
	for _, snapshot := range snapshots {
		require.NotContains(t, snapshot, "ITEM-NOTE-CANARY")
		require.NotContains(t, snapshot, "LIST-NOTE-CANARY")
	}
	listInput := share.ListInput{Type: "customer", CustomerID: &customer.ID, Name: "New name", Currency: "USD", Status: "active", Version: 2}
	r, _ = f.request(t, "PUT", path, listInput)
	require.Equal(t, 400, r.Code, "cannot relabel existing SKU prices into another currency")
	listInput.Currency = "CNY"
	r, _ = f.request(t, "PUT", path, listInput)
	require.Equal(t, 200, r.Code, r.Body.String())
	r, e = f.request(t, "PUT", path, listInput)
	require.Equal(t, 409, r.Code)
	require.Equal(t, 10409, e.Code)
	r, _ = f.request(t, "DELETE", path+"?version=3", nil)
	require.Equal(t, 204, r.Code)
	r, _ = f.request(t, "GET", path, nil)
	require.Equal(t, 404, r.Code)
	out := f.preview(t, f.input(customer.ID, 0))
	require.EqualValues(t, 900, *out[0].PriceCents)
}

func TestCustomerIntegrationContactEnvelopesAuthenticateEveryDimension(t *testing.T) {
	f := newShareFixture(t)
	view := f.customer(t, "envelopes")
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var row share.Customer
	require.NoError(t, q.Where("id=?", view.ID).Take(&row).Error)
	fields := []struct {
		name, expected string
		envelope       share.Envelope
	}{{"contact_name", "CONTACT-CANARY", row.ContactName}, {"phone", "PHONE-CANARY", row.Phone}, {"email", "EMAIL-CANARY", row.Email}}
	for _, field := range fields {
		aad, err := share.FieldAAD(1, "customer", row.ID, field.name, row.Version)
		require.NoError(t, err)
		plain, err := f.sealer.Open(field.envelope.Cipher, field.envelope.Nonce, aad, row.KeyVersion)
		require.NoError(t, err)
		require.Equal(t, field.expected, string(plain))
		clear(plain)
		for _, dims := range []struct {
			tenant, id    int64
			entity, field string
			revision      int
		}{{2, row.ID, "customer", field.name, row.Version}, {1, row.ID + 1, "customer", field.name, row.Version},
			{1, row.ID, "customer", field.name, row.Version + 1}, {1, row.ID, "customer", map[string]string{"contact_name": "phone", "phone": "email", "email": "contact_name"}[field.name], row.Version}} {
			aad, err := share.FieldAAD(dims.tenant, dims.entity, dims.id, dims.field, dims.revision)
			require.NoError(t, err)
			_, err = f.sealer.Open(field.envelope.Cipher, field.envelope.Nonce, aad, row.KeyVersion)
			require.Error(t, err)
		}
	}
	require.NotEqual(t, row.Phone.Nonce, row.Email.Nonce)
	require.NotEqual(t, row.Phone.Nonce, row.ContactName.Nonce)
}

func TestCustomerIntegrationDeletePreservesContactEnvelope(t *testing.T) {
	// REQ-SHR-001: a tombstone must retain the version authenticating its ciphertext.
	f := newShareFixture(t)
	view := f.customer(t, "deleted-envelope")
	r, _ := f.request(t, "DELETE", fmt.Sprintf("/customers/%d?version=%d", view.ID, view.Version), nil)
	require.Equal(t, 204, r.Code)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var row share.Customer
	require.NoError(t, q.Where("id=?", view.ID).Take(&row).Error)
	require.NotNil(t, row.DeletedAt)
	aad, err := share.FieldAAD(1, "customer", row.ID, "phone", row.Version)
	require.NoError(t, err)
	plain, err := f.sealer.Open(row.Phone.Cipher, row.Phone.Nonce, aad, row.KeyVersion)
	require.NoError(t, err)
	defer clear(plain)
	require.Equal(t, "PHONE-CANARY", string(plain))
}
