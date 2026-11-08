package bootstrap

import (
	"bytes"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestShareInquiryIntegrationCompletionTimeHoneypotAndLargeBody(t *testing.T) {
	f := newShareAccessFixture(t)
	link, cookie, in := admittedInquiry(t, f)
	f.now = f.now.Add(-3 * time.Second)
	r := inquiryRequest(t, f, link, cookie, "192.0.2.50", "early", "https://quote.test", in)
	require.Equal(t, 404, r.Code)
	f.now = f.now.Add(3 * time.Second)
	in.Website = "synthetic-trap"
	r = inquiryRequest(t, f, link, cookie, "192.0.2.50", "trap", "https://quote.test", in)
	require.Equal(t, 404, r.Code)
	for _, chunked := range []bool{false, true} {
		req := httptest.NewRequest("POST", "https://quote.test"+link.URL+"/inquiry", strings.NewReader(strings.Repeat("x", (1<<20)+1)))
		req.Header.Set("Origin", "https://quote.test")
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(cookie)
		if chunked {
			req.ContentLength = -1
		}
		w := httptest.NewRecorder()
		f.router.ServeHTTP(w, req)
		require.Equal(t, 404, w.Code)
		require.Contains(t, w.Body.String(), `"message":"链接暂不可用"`)
		require.Contains(t, w.Body.String(), `"data":null`)
	}
	rawCount(f.ctx, t, f.database, "inquiry", 0)
}

func TestShareInquiryIntegrationExpiredReceiptAndAdminIdempotency(t *testing.T) {
	f := newShareAccessFixture(t)
	link, cookie, in := admittedInquiry(t, f)
	for range 2 {
		r := inquiryRequest(t, f, link, cookie, "192.0.2.50", "expired", "https://quote.test", in)
		require.Equal(t, 201, r.Code, r.Body.String())
		q, cancel := f.database.WithContext(f.ctx)
		require.NoError(t, q.Table("share_inquiry_receipt").Where("tenant_id=1").UpdateColumn("expires_at", f.now.Add(-time.Second)).Error)
		cancel()
	}
	rawCount(f.ctx, t, f.database, "inquiry", 2)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var id int64
	require.NoError(t, q.Table("inquiry").Select("id").Order("id").Limit(1).Take(&id).Error)
	path := fmt.Sprintf("/inquiries/%d", id)
	for range 2 {
		r, _ := f.keyed(t, "PUT", path, "state-change", map[string]any{"status": "contacted"})
		require.Equal(t, 200, r.Code, r.Body.String())
	}
	r, _ := f.keyed(t, "PUT", path, "state-change", map[string]any{"status": "quoted"})
	require.Equal(t, 409, r.Code)
	require.Contains(t, r.Body.String(), `"code":10006`)
	require.NoError(t, q.Table("sys_role").Where("tenant_id=1").Update("data_scope", "self").Error)
	require.NoError(t, q.Table("inquiry").Where("id=?", id).Update("owner_user_id", f.user.ID).Error)
	r, _ = f.keyed(t, "PUT", path, "state-change", map[string]any{"status": "contacted"})
	require.Equal(t, 404, r.Code, "replay rechecks live scope")
}

func TestShareInquiryIntegrationSubmissionRejectsForwardedHeaderSpoof(t *testing.T) {
	f := newShareAccessFixture(t)
	link, cookie, _ := admittedInquiry(t, f)
	for i := 0; i < 4; i++ {
		req := httptest.NewRequest("POST", "https://quote.test"+link.URL+"/inquiry", bytes.NewBufferString(`{"contact_name":"synthetic","contact_phone":"synthetic"}`))
		req.RemoteAddr = "192.0.2.70:80"
		req.Header.Set("Origin", "https://quote.test")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Forwarded-For", fmt.Sprintf("198.51.100.%d", i+1))
		req.Header.Set("Idempotency-Key", fmt.Sprint(i))
		req.AddCookie(cookie)
		w := httptest.NewRecorder()
		f.router.ServeHTTP(w, req)
		want := 201
		if i == 3 {
			want = 429
		}
		require.Equal(t, want, w.Code)
	}
}
