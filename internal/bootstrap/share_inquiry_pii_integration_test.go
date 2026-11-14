package bootstrap

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/share"
	"github.com/stretchr/testify/require"
)

func submitInquiryID(t *testing.T, f *shareAccessFixture) int64 {
	t.Helper()
	link, cookie, in := admittedInquiry(t, f)
	r := inquiryRequest(t, f, link, cookie, "192.0.2.50", "submitted", "https://quote.test", in)
	require.Equal(t, 201, r.Code, r.Body.String())
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var id int64
	require.NoError(t, q.Table("inquiry").Select("id").Take(&id).Error)
	return id
}

func reauthInquiry(t *testing.T, f *shareAccessFixture) {
	t.Helper()
	r, _ := authRequest(t, f.router, "/api/v1/auth/reauth", map[string]string{"password": f.password}, f.bearer)
	require.Equal(t, 200, r.Code, r.Body.String())
}

func inquiryPII(t *testing.T, f *shareAccessFixture, id int64) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", fmt.Sprintf("/api/v1/admin/inquiries/%d/pii", id), nil)
	req.Header.Set("Authorization", "Bearer "+f.bearer)
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	return w
}

func TestShareInquiryIntegrationMaskPermissionReauthAndAudit(t *testing.T) {
	f := newShareAccessFixture(t)
	id := submitInquiryID(t, f)
	r, _ := f.request(t, "GET", "/inquiries", nil)
	require.Equal(t, 200, r.Code)
	require.Contains(t, r.Body.String(), `"contact_phone":"***********"`)
	r = inquiryPII(t, f, id)
	require.Equal(t, 403, r.Code)
	reauthInquiry(t, f)
	r = inquiryPII(t, f, id)
	require.Equal(t, 200, r.Code, r.Body.String())
	require.Equal(t, "private, no-store", r.Header().Get("Cache-Control"))
	var result struct {
		Data share.InquiryInput `json:"data"`
	}
	require.NoError(t, json.Unmarshal(r.Body.Bytes(), &result))
	require.Contains(t, f.markers, result.Data.ContactName)
	f.now = f.now.Add(5 * time.Minute)
	r = inquiryPII(t, f, id)
	require.Equal(t, 403, r.Code, "reauth exactly five minutes old must fail")
	reauthInquiry(t, f)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.Table("sys_user").Where("id=?", f.actor.UserID).Update("is_super", false).Error)
	require.NoError(t, q.Exec("DELETE FROM sys_role_permission WHERE permission_id IN (SELECT id FROM sys_permission WHERE tenant_id=1 AND code=?)", "trade:inquiry:pii:read").Error)
	r = inquiryPII(t, f, id)
	require.Equal(t, 403, r.Code)
	require.Contains(t, r.Body.String(), `"code":20107`)
	f.now = f.now.Add(5 * time.Minute)
	r = inquiryPII(t, f, id)
	require.Equal(t, 403, r.Code)
	var audits []string
	require.NoError(t, q.Raw("SELECT row_to_json(a)::text FROM audit_log a WHERE action='share.inquiry.pii.read' ORDER BY id").Scan(&audits).Error)
	require.Len(t, audits, 5)
	for _, row := range audits {
		f.noLeaks(t, "PII audit", row)
		for _, field := range []string{"actor_id", "resource_id", "created_at", "contact_name", "contact_phone", "company", "message", "items"} {
			require.Contains(t, row, field)
		}
	}
	t.Log("masked list; permission-only=403; permission+recent=200; recent-only=403; neither=403; age=5m denied; five safe who/record/fields/time audits")
}

func TestShareInquiryIntegrationTamperAndAuditFailure(t *testing.T) {
	f := newShareAccessFixture(t)
	id := submitInquiryID(t, f)
	reauthInquiry(t, f)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	for _, field := range []string{"contact_name", "contact_phone", "contact_email", "company", "message", "items"} {
		var cipher []byte
		require.NoError(t, q.Table("inquiry").Select(field+"_cipher").Where("id=?", id).Row().Scan(&cipher))
		corrupt := bytes.Clone(cipher)
		corrupt[0] ^= 1
		require.NoError(t, q.Table("inquiry").Where("id=?", id).Update(field+"_cipher", corrupt).Error)
		r := inquiryPII(t, f, id)
		require.Equal(t, 503, r.Code)
		f.noLeaks(t, "tamper error", r.Body.String())
		require.NoError(t, q.Table("inquiry").Where("id=?", id).Update(field+"_cipher", cipher).Error)
	}
	require.NoError(t, f.database.Exec(f.ctx, `CREATE FUNCTION reject_inquiry_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='share.inquiry.pii.read' THEN RAISE EXCEPTION 'audit unavailable'; END IF; RETURN NEW; END $$`))
	require.NoError(t, f.database.Exec(f.ctx, `CREATE TRIGGER reject_inquiry_audit BEFORE INSERT ON audit_log FOR EACH ROW EXECUTE FUNCTION reject_inquiry_audit()`))
	r := inquiryPII(t, f, id)
	require.Equal(t, 503, r.Code)
	f.noLeaks(t, "audit failure", r.Body.String())
	require.Contains(t, r.Body.String(), `"code":10002`)
	t.Log("six corrupted field envelopes and audit write failure never return partial PII")
}

func TestShareInquiryIntegrationCurrentScopeAndConversion(t *testing.T) {
	f := newShareAccessFixture(t)
	id := submitInquiryID(t, f)
	reauthInquiry(t, f)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	ordinaryScopeActor(t, f.identityFixture, f.actor.UserID)
	require.NoError(t, q.Table("sys_role").Where("tenant_id=1").Update("data_scope", "self").Error)
	require.NoError(t, q.Table("inquiry").Where("id=?", id).Update("owner_user_id", f.user.ID).Error)
	r := inquiryPII(t, f, id)
	require.Equal(t, 404, r.Code)
	r, _ = f.request(t, "GET", "/inquiries", nil)
	require.Contains(t, r.Body.String(), `"total":0`)
	require.NoError(t, q.Table("inquiry").Where("id=?", id).Update("owner_user_id", f.actor.UserID).Error)
	r, _ = f.request(t, "PUT", fmt.Sprintf("/inquiries/%d", id), map[string]any{"status": "won"})
	require.Equal(t, 200, r.Code)
	var converted time.Time
	require.NoError(t, q.Table("inquiry").Select("converted_at").Where("id=?", id).Row().Scan(&converted))
	f.now = f.now.Add(time.Minute)
	r, _ = f.request(t, "PUT", fmt.Sprintf("/inquiries/%d", id), map[string]any{"status": "won"})
	require.Equal(t, 200, r.Code)
	var preserved time.Time
	require.NoError(t, q.Table("inquiry").Select("converted_at").Where("id=?", id).Row().Scan(&preserved))
	require.True(t, converted.Equal(preserved))
	r, _ = f.request(t, "PUT", fmt.Sprintf("/inquiries/%d", id), map[string]any{"status": "new"})
	require.Equal(t, 409, r.Code)
	f.noLeaks(t, "workflow response", r.Body.String())
	require.False(t, strings.Contains(r.Body.String(), "CANARY"))
	t.Log("current owner scope excludes rows/counts; first won conversion timestamp survives edits; terminal status cannot reopen")
}
