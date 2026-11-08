package bootstrap

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/share"
	"github.com/stretchr/testify/require"
)

func inquiryRequest(t *testing.T, f *shareAccessFixture, link share.LinkReceipt, cookie *http.Cookie, ip, key, origin string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	request := httptest.NewRequest("POST", "https://quote.test"+link.URL+"/inquiry", bytes.NewReader(raw))
	request.RemoteAddr = ip + ":32100"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", origin)
	request.Header.Set("Idempotency-Key", key)
	if cookie != nil {
		request.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, request)
	f.noLeaks(t, "inquiry response", w.Body.String())
	return w
}

func admittedInquiry(t *testing.T, f *shareAccessFixture) (share.LinkReceipt, *http.Cookie, share.InquiryInput) {
	t.Helper()
	link := f.issue(t, f.publication())
	root := f.anonymous(t, "GET", link.URL, "192.0.2.50", nil, nil)
	cookie := shareCookie(t, root)
	f.now = f.now.Add(3 * time.Second)
	return link, cookie, share.InquiryInput{ContactName: f.randomValue(t), ContactPhone: f.randomValue(t), ContactEmail: f.randomValue(t),
		Company: f.randomValue(t), Message: f.randomValue(t), Items: []share.InquiryItem{{SPUID: f.spu, SKUID: f.sku, Qty: 5, Note: f.randomValue(t)}}}
}

func TestShareInquiryIntegrationSubmissionIdempotencyAndCanary(t *testing.T) {
	// REQ-SHR-008: only authenticated field envelopes leave intake memory.
	f := newShareAccessFixture(t)
	recordShareSQLTemplates(t, f)
	link, cookie, input := admittedInquiry(t, f)
	for range 2 {
		r := inquiryRequest(t, f, link, cookie, "192.0.2.50", "same", "https://quote.test", input)
		require.Equal(t, 201, r.Code, r.Body.String())
		var envelope struct {
			Data map[string]any `json:"data"`
		}
		require.NoError(t, json.Unmarshal(r.Body.Bytes(), &envelope))
		require.Equal(t, map[string]any{"submitted": true}, envelope.Data)
	}
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	for _, table := range []string{"inquiry", "notification", "outbox_event", "share_inquiry_receipt"} {
		var records []string
		require.NoError(t, q.Raw("SELECT row_to_json(t)::text FROM "+table+" t").Scan(&records).Error)
		require.Len(t, records, 1, table)
		f.noLeaks(t, table, strings.Join(records, ""))
	}
	var stored share.Inquiry
	require.NoError(t, q.Table("inquiry").Take(&stored).Error)
	for field, envelope := range map[string]share.Envelope{"contact_name": stored.ContactName, "contact_phone": stored.ContactPhone, "contact_email": stored.ContactEmail, "company": stored.Company, "message": stored.Message, "items": stored.Items} {
		aad, err := share.InquiryAAD(1, stored.ID, field, stored.Revision)
		require.NoError(t, err)
		plain, err := f.sealer.Open(envelope.Cipher, envelope.Nonce, aad, stored.KeyVersion)
		require.NoError(t, err)
		require.NotEmpty(t, plain)
		clear(plain)
	}
	input.Message = "different"
	r := inquiryRequest(t, f, link, cookie, "192.0.2.50", "same", "https://quote.test", input)
	require.Equal(t, 409, r.Code)
	for _, path := range []string{link.URL + "/inquiry", link.URL + "/inquiry/1", "/api/v1/admin/inquiries"} {
		w := httptest.NewRecorder()
		f.router.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		require.GreaterOrEqual(t, w.Code, 400)
		f.noLeaks(t, "anonymous lookup", w.Body.String())
	}
	req := httptest.NewRequest("GET", "/api/v1/admin/inquiries/export", nil)
	req.Header.Set("Authorization", "Bearer "+f.bearer)
	r = httptest.NewRecorder()
	f.router.ServeHTTP(r, req)
	require.Equal(t, 200, r.Code)
	require.Equal(t, "id,share_link_id,status,created_at,converted_at,pii_purged_at", strings.Split(r.Body.String(), "\n")[0])
	f.noLeaks(t, "application log", f.logs.text())
	f.noLeaks(t, "SQL log", f.sqlLogs.text())
	require.Contains(t, f.sqlLogs.text(), `INSERT INTO "inquiry"`)
	require.Contains(t, f.sqlLogs.text(), `INSERT INTO "notification"`)
	t.Log("201 submitted=true; six field envelopes verified; replay=one inquiry/inbox/outbox; mismatch=409; anonymous lookup denied; DB/log/error/CSV/inbox/outbox/receipt canaries absent")
}

func TestShareInquiryIntegrationSourceBudgetAndAdmissionGuards(t *testing.T) {
	f := newShareAccessFixture(t)
	link, cookie, in := admittedInquiry(t, f)
	for i := 1; i <= 4; i++ {
		r := inquiryRequest(t, f, link, cookie, "192.0.2.50", fmt.Sprint(i), "https://quote.test", in)
		want := 201
		if i == 4 {
			want = 429
		}
		require.Equal(t, want, r.Code, r.Body.String())
	}
	r := inquiryRequest(t, f, link, cookie, "192.0.2.51", "other", "https://quote.test", in)
	require.Equal(t, 201, r.Code)
	r = inquiryRequest(t, f, link, cookie, "192.0.2.50", "1", "https://quote.test", in)
	require.Equal(t, 201, r.Code, "safe replay survives exhausted submission budget")
	for _, origin := range []string{"", "null", "https://other.test", "https://quote.test/path", "https://quote.test?x=1"} {
		r = inquiryRequest(t, f, link, cookie, "192.0.2.52", "origin", origin, in)
		require.Equal(t, 404, r.Code)
	}
	r = inquiryRequest(t, f, link, nil, "192.0.2.52", "cookie", "https://quote.test", in)
	require.Equal(t, 404, r.Code)
	in.Items[0].SPUID++
	r = inquiryRequest(t, f, link, cookie, "192.0.2.52", "member", "https://quote.test", in)
	require.Equal(t, 404, r.Code)
	in.Message = strings.Repeat("x", 17<<10)
	r = inquiryRequest(t, f, link, cookie, "192.0.2.52", "body", "https://quote.test", in)
	require.Equal(t, 404, r.Code)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.Table("share_link").Where("id=?", link.ID).Update("allow_inquiry", false).Error)
	r = inquiryRequest(t, f, link, cookie, "192.0.2.53", "disabled", "https://quote.test", share.InquiryInput{ContactName: "synthetic", ContactPhone: "synthetic"})
	require.Equal(t, 404, r.Code)
	t.Log("source A submissions 1/2/3=201, 4=429; source B=201; replay=201; origin/cookie/member/body/allow_inquiry failures use one safe message")
}

func TestShareInquiryIntegrationAtomicInboxFailure(t *testing.T) {
	f := newShareAccessFixture(t)
	link, cookie, in := admittedInquiry(t, f)
	require.NoError(t, f.database.Exec(f.ctx, `CREATE FUNCTION reject_inquiry_inbox() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'inbox unavailable'; END $$`))
	require.NoError(t, f.database.Exec(f.ctx, `CREATE TRIGGER reject_inquiry_inbox BEFORE INSERT ON notification FOR EACH ROW EXECUTE FUNCTION reject_inquiry_inbox()`))
	r := inquiryRequest(t, f, link, cookie, "192.0.2.50", "atomic", "https://quote.test", in)
	require.Equal(t, 503, r.Code)
	for _, table := range []string{"inquiry", "share_inquiry_receipt", "notification", "outbox_event"} {
		rawCount(f.ctx, t, f.database, table, 0)
	}
	t.Log("inbox failure=503; inquiry/receipt/notification/outbox rolled back; no PII in safe error")
}

func TestShareInquiryIntegrationAtomicAuditFailure(t *testing.T) {
	f := newShareAccessFixture(t)
	link, cookie, in := admittedInquiry(t, f)
	require.NoError(t, f.database.Exec(f.ctx, `CREATE FUNCTION reject_inquiry_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='share.inquiry.submit' THEN RAISE EXCEPTION 'audit unavailable'; END IF; RETURN NEW; END $$`))
	require.NoError(t, f.database.Exec(f.ctx, `CREATE TRIGGER reject_inquiry_audit BEFORE INSERT ON audit_log FOR EACH ROW EXECUTE FUNCTION reject_inquiry_audit()`))
	r := inquiryRequest(t, f, link, cookie, "192.0.2.50", "audit-atomic", "https://quote.test", in)
	require.Equal(t, 503, r.Code)
	for _, table := range []string{"inquiry", "share_inquiry_receipt", "notification", "outbox_event"} {
		rawCount(f.ctx, t, f.database, table, 0)
	}
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.Exec("DROP TRIGGER reject_inquiry_audit ON audit_log").Error)
	r = inquiryRequest(t, f, link, cookie, "192.0.2.50", "audit-atomic", "https://quote.test", in)
	require.Equal(t, 201, r.Code)
	for _, table := range []string{"inquiry", "share_inquiry_receipt", "notification", "outbox_event"} {
		rawCount(f.ctx, t, f.database, table, 1)
	}
	t.Log("submission audit failure=503 rolls inquiry/receipt/inbox/outbox back; same-key retry commits once after recovery")
}
