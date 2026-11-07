package bootstrap

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/order"
	"github.com/stretchr/testify/require"
)

func orderPIIReauth(t *testing.T, f *orderQueryFixture) {
	t.Helper()
	response, _ := authRequest(t, f.router, "/api/v1/auth/reauth", map[string]string{"password": f.password}, f.bearer)
	require.Equal(t, 200, response.Code, response.Body.String())
}

func (f *orderQueryFixture) piiRequest(t *testing.T, id int64) (*httptest.ResponseRecorder, shopHTTPEnvelope) {
	t.Helper()
	return shopHTTPRequest(t, f.router, "GET", fmt.Sprintf("/api/v1/admin/orders/%d/address", id), nil, f.bearer)
}

func TestOrderPIIIntegrationAuthenticationPermissionMatrixAndAudit(t *testing.T) {
	// REQ-ORD-006: permission and current-session reauthentication are independent.
	f := newOrderQueryFixture(t)
	s := f.orders.snapshot(t, "pii-matrix")
	s.Address = &order.AddressInput{ReceiverName: []byte("张合成"), Phone: []byte("1-CANARY"), Address: []byte("PII-CANARY-123")}
	result, err := f.orders.apply(s)
	require.NoError(t, err)
	r, e := f.piiRequest(t, result.OrderID)
	require.Equal(t, 403, r.Code)
	require.Equal(t, 20108, e.Code)
	require.NotContains(t, r.Body.String(), "PII-CANARY-123")
	orderPIIReauth(t, f)
	r, e = f.piiRequest(t, result.OrderID)
	require.Equal(t, 200, r.Code, r.Body.String())
	require.Equal(t, "no-store", r.Header().Get("Cache-Control"))
	var disclosed order.AddressView
	require.NoError(t, json.Unmarshal(e.Data, &disclosed))
	require.Equal(t, "PII-CANARY-123", disclosed.Address)
	f.now = f.now.Add(5 * time.Minute)
	r, e = f.piiRequest(t, result.OrderID)
	require.Equal(t, 403, r.Code)
	require.Equal(t, 20108, e.Code)
	f.now = f.now.Add(time.Second)
	orderPIIReauth(t, f)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.Table("sys_user").Where("id=?", f.actor.UserID).Update("is_super", false).Error)
	require.NoError(t, q.Exec(`DELETE FROM sys_role_permission WHERE permission_id IN (SELECT id FROM sys_permission WHERE tenant_id=1 AND code=?)`, "trade:order:pii:read").Error)
	r, e = f.piiRequest(t, result.OrderID)
	require.Equal(t, 403, r.Code)
	require.Equal(t, 20107, e.Code)
	f.now = f.now.Add(5 * time.Minute)
	r, e = f.piiRequest(t, result.OrderID)
	require.Equal(t, 403, r.Code)
	require.Equal(t, 20107, e.Code)
	var audits []string
	require.NoError(t, q.Raw(`SELECT row_to_json(a)::text FROM audit_log a WHERE action='order.pii.read' ORDER BY id`).Scan(&audits).Error)
	require.Len(t, audits, 5)
	for _, record := range audits {
		require.NotContains(t, record, "PII-CANARY-123")
		require.Contains(t, record, `"fields": ["receiver_name", "phone", "address"]`)
		require.Contains(t, record, `"actor_id":`)
		require.Contains(t, record, `"created_at":`)
	}
	require.Contains(t, audits[1], `"result":"success"`)
	t.Log("all four permission/reauth combinations verified; age>=5m=403/20108; five PII-free audits")
}

func TestOrderPIIIntegrationTamperedFieldsAndAuditFailureNeverDisclose(t *testing.T) {
	f := newOrderQueryFixture(t)
	s := f.orders.snapshot(t, "pii-tamper")
	s.Address = &order.AddressInput{ReceiverName: []byte("张合成"), Phone: []byte("1-CANARY"), Address: []byte("PII-CANARY-123")}
	result, err := f.orders.apply(s)
	require.NoError(t, err)
	orderPIIReauth(t, f)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	for _, column := range []string{"receiver_name_cipher", "phone_cipher", "address_cipher"} {
		var stored struct{ Cipher []byte }
		require.NoError(t, q.Table("order_address").Select(column+" AS cipher").Where("order_id=?", result.OrderID).Take(&stored).Error)
		corrupt := bytes.Clone(stored.Cipher)
		corrupt[0] ^= 1
		require.NoError(t, q.Table("order_address").Where("order_id=?", result.OrderID).Update(column, corrupt).Error)
		r, e := f.piiRequest(t, result.OrderID)
		require.Equal(t, 500, r.Code)
		require.Equal(t, 50401, e.Code)
		require.Empty(t, e.Data)
		for _, plain := range [][]byte{s.Address.ReceiverName, s.Address.Phone, s.Address.Address} {
			require.NotContains(t, r.Body.String(), string(plain))
		}
		require.NoError(t, q.Table("order_address").Where("order_id=?", result.OrderID).Update(column, stored.Cipher).Error)
	}
	require.NoError(t, f.database.Exec(f.ctx, `CREATE FUNCTION reject_order_pii_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='order.pii.read' THEN RAISE EXCEPTION 'audit unavailable'; END IF; RETURN NEW; END $$`))
	require.NoError(t, f.database.Exec(f.ctx, `CREATE TRIGGER reject_order_pii_audit BEFORE INSERT ON audit_log FOR EACH ROW EXECUTE FUNCTION reject_order_pii_audit()`))
	r, e := f.piiRequest(t, result.OrderID)
	require.Equal(t, 500, r.Code)
	require.Empty(t, e.Data)
	require.NotContains(t, r.Body.String(), "PII-CANARY-123")
	t.Log("each of three tampered ciphertexts=500/50401 without partial fields; audit failure=500 without disclosure")
}

func TestOrderPIIIntegrationLiveShopScopeAndSessionIsolation(t *testing.T) {
	f := newOrderQueryFixture(t)
	id := f.add(t, "pii-scope", time.Now().UTC())
	orderPIIReauth(t, f)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.Table("sys_role").Where("tenant_id=1 AND code=?", identity.SuperAdminRoleCode).Update("data_scope", "self").Error)
	require.NoError(t, q.Table("shop").Where("id=?", f.shop.ID).Update("owner_user_id", f.user.ID).Error)
	r, e := f.piiRequest(t, id)
	require.Equal(t, 404, r.Code)
	require.Equal(t, 50101, e.Code)
	require.NoError(t, q.Table("shop").Where("id=?", f.shop.ID).Update("owner_user_id", f.actor.UserID).Error)
	other, err := f.identityFixture.service.Login(f.ctx, 1, identity.SeedAdminUsername, f.password, identity.RequestMeta{})
	require.NoError(t, err)
	f.bearer = other.AccessToken
	r, e = f.piiRequest(t, id)
	require.Equal(t, 403, r.Code)
	require.Equal(t, 20108, e.Code)
}
