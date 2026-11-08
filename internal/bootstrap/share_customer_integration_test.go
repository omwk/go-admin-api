package bootstrap

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/share"
	"github.com/stretchr/testify/require"
)

func TestCustomerIntegrationCRUDVersionUniquenessAndMaskedIdempotency(t *testing.T) {
	// REQ-SHR-001: private fields never appear in response, audit or replay storage.
	f := newShareFixture(t)
	in := share.CustomerInput{Code: "crud", Name: "Customer", ContactName: "CONTACT-CANARY", Phone: "PHONE-CANARY", Email: "EMAIL-CANARY"}
	r, e := f.keyed(t, "POST", "/customers", "customer-create", in)
	require.Equal(t, 201, r.Code, r.Body.String())
	var row share.CustomerView
	require.NoError(t, json.Unmarshal(e.Data, &row))
	require.Equal(t, "***********", row.Phone)
	replay, _ := f.keyed(t, "POST", "/customers", "customer-create", in)
	require.Equal(t, r.Body.String(), replay.Body.String())
	in.Name = "Changed"
	r, e = f.keyed(t, "POST", "/customers", "customer-create", in)
	require.Equal(t, 409, r.Code)
	require.Equal(t, 10006, e.Code)
	r, e = f.request(t, "POST", "/customers", in)
	require.Equal(t, 409, r.Code)
	require.Equal(t, 10004, e.Code)
	path := fmt.Sprintf("/customers/%d", row.ID)
	in.Version = row.Version
	r, e = f.request(t, "PUT", path, in)
	require.Equal(t, 200, r.Code, r.Body.String())
	r, e = f.request(t, "PUT", path, in)
	require.Equal(t, 409, r.Code)
	require.Equal(t, 10409, e.Code)
	r, e = f.request(t, "GET", "/customers?page_size=1", nil)
	require.Equal(t, 200, r.Code)
	var page share.Page[share.CustomerView]
	require.NoError(t, json.Unmarshal(e.Data, &page))
	require.EqualValues(t, 1, page.Total)
	require.Len(t, page.List, 1)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var persisted []string
	require.NoError(t, q.Raw(`SELECT row_to_json(c)::text FROM customer c UNION ALL SELECT row_to_json(a)::text FROM audit_log a UNION ALL SELECT row_to_json(i)::text FROM idempotency_key i`).Scan(&persisted).Error)
	for _, record := range append(persisted, r.Body.String(), f.logs.text(), f.sqlLogs.text()) {
		for _, marker := range []string{in.ContactName, in.Phone, in.Email} {
			require.NotContains(t, record, marker)
		}
	}
	r, e = f.request(t, "DELETE", path+"?version=1", nil)
	require.Equal(t, 409, r.Code)
	require.Equal(t, 10409, e.Code)
	r, _ = f.request(t, "DELETE", path+"?version=2", nil)
	require.Equal(t, 204, r.Code)
	r, _ = f.request(t, "GET", path, nil)
	require.Equal(t, 404, r.Code)
	var deleted bool
	require.NoError(t, q.Raw("SELECT deleted_at IS NOT NULL FROM customer WHERE id=?", row.ID).Scan(&deleted).Error)
	require.True(t, deleted)
}

func TestCustomerIntegrationOwnerScope404AndPermission403(t *testing.T) {
	f := newShareFixture(t)
	visible, hidden := f.customer(t, "visible"), f.customer(t, "hidden")
	list := f.list(t, &hidden.ID)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.Table("customer").Where("id=?", hidden.ID).Update("owner_user_id", f.user.ID).Error)
	require.NoError(t, q.Table("sys_role").Where("tenant_id=1 AND code=?", identity.SuperAdminRoleCode).Update("data_scope", "self").Error)
	for _, path := range []string{fmt.Sprintf("/customers/%d", hidden.ID), fmt.Sprintf("/price-lists/%d", list.ID)} {
		r, _ := f.request(t, "GET", path, nil)
		require.Equal(t, 404, r.Code, r.Body.String())
		r, _ = f.request(t, "DELETE", path+"?version=1", nil)
		require.Equal(t, 404, r.Code, r.Body.String())
	}
	r, _ := f.request(t, "PUT", fmt.Sprintf("/customers/%d", hidden.ID), share.CustomerInput{Code: hidden.Code, Name: hidden.Name, Version: hidden.Version})
	require.Equal(t, 404, r.Code)
	r, _ = f.request(t, "POST", fmt.Sprintf("/price-lists/%d/items:batch", list.ID), share.BatchInput{Version: list.Version, Items: []share.ItemInput{{SPUID: f.spu, PriceCents: 1}}})
	require.Equal(t, 404, r.Code)
	r, _ = f.request(t, "POST", "/price-preview", f.input(hidden.ID, 0))
	require.Equal(t, 404, r.Code)
	r, e := f.request(t, "GET", "/customers", nil)
	require.Equal(t, 200, r.Code)
	var page share.Page[share.CustomerView]
	require.NoError(t, json.Unmarshal(e.Data, &page))
	require.EqualValues(t, 1, page.Total)
	require.Equal(t, visible.ID, page.List[0].ID)
	require.NoError(t, q.Table("sys_user").Where("id=?", f.actor.UserID).Update("is_super", false).Error)
	require.NoError(t, q.Exec(`DELETE FROM sys_role_permission WHERE permission_id IN (SELECT id FROM sys_permission WHERE tenant_id=1 AND code LIKE 'trade:%')`).Error)
	for _, path := range []string{"/customers", "/price-lists"} {
		r, e := f.request(t, "GET", path, nil)
		require.Equal(t, 403, r.Code)
		require.Equal(t, 20107, e.Code)
	}
	r, e = f.request(t, "POST", "/price-preview", f.input(visible.ID, 0))
	require.Equal(t, 403, r.Code)
	require.Equal(t, 20107, e.Code)
}

func TestCustomerIntegrationCrossTenantAndOwnerAssignment(t *testing.T) {
	f := newShareFixture(t)
	row := f.customer(t, "tenant-one")
	list := f.list(t, &row.ID)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.Exec("INSERT INTO tenant(id,code,name) OVERRIDING SYSTEM VALUE VALUES (2,'pricing-other','Other')").Error)
	require.NoError(t, q.Table("customer").Where("id=?", row.ID).Update("tenant_id", 2).Error)
	for _, path := range []string{fmt.Sprintf("/customers/%d", row.ID), fmt.Sprintf("/price-lists/%d", list.ID)} {
		r, _ := f.request(t, "GET", path, nil)
		require.Equal(t, 404, r.Code)
	}
	r, _ := f.request(t, "POST", "/price-preview", f.input(row.ID, list.ID))
	require.Equal(t, 404, r.Code)
	r, _ = f.request(t, "POST", "/customers", share.CustomerInput{Code: "bad-owner", Name: "Owner", OwnerUserID: 999999})
	require.Equal(t, 404, r.Code)
	require.NoError(t, q.Table("sys_role").Where("tenant_id=1 AND code=?", identity.SuperAdminRoleCode).Update("data_scope", "self").Error)
	r, _ = f.request(t, "POST", "/customers", share.CustomerInput{Code: "other-owner", Name: "Owner", OwnerUserID: f.user.ID})
	require.Equal(t, 404, r.Code)
}
