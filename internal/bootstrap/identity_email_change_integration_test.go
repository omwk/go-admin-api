package bootstrap

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/refresh"
	"github.com/stretchr/testify/require"
)

func emailChangeFixture(t *testing.T) (*accountFixture, *accountMailCapture, string, string) {
	t.Helper()
	f := profileFieldsFixture(t)
	capture := accountMailProvider(t, f)
	old := *accountUser(t, f, f.user.ID).Email
	return f, capture, old, strings.ToLower(accountPassword(t)) + "@example.invalid"
}

func emailChangeRequest(t *testing.T, f *accountFixture, email string) {
	t.Helper()
	w := f.request(t, "POST", "/auth/profile/email/change", f.token, map[string]any{"email": email})
	require.Equal(t, 202, w.Code)
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	var result struct {
		Pending bool `json:"pending"`
		Hours   int  `json:"expires_in_hours"`
	}
	notificationDataF3(t, w, &result)
	require.True(t, result.Pending)
	require.Equal(t, 24, result.Hours)
	validateOpenAPIResponse(t, readIdentityOpenAPI(t), "/api/v1/auth/profile/email/change", "post", w.Code, w.Body.Bytes())
}

func emailChangeDeliver(t *testing.T, f *accountFixture) {
	t.Helper()
	for _, code := range []string{"identity.email_change", "identity.email_change_notice"} {
		out := accountOutbox(t, f, code)
		d, err := f.notifications.delivery.Deliver(f.ctx, 1, out.ID)
		require.NoError(t, err)
		require.Equal(t, "sent", d.Status)
	}
}

func emailChangeVerify(t *testing.T, f *accountFixture, raw string, status int) {
	t.Helper()
	w := f.request(t, "POST", "/auth/profile/email/verify", "", map[string]any{"token": raw})
	require.Equal(t, status, w.Code)
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	validateOpenAPIResponse(t, readIdentityOpenAPI(t), "/api/v1/auth/profile/email/verify", "post", w.Code, w.Body.Bytes())
}

func emailChangeLogin(t *testing.T, f *accountFixture, email string, status int) {
	t.Helper()
	w := f.request(t, "POST", "/auth/login", "", map[string]any{"tenant": "default", "username": email, "password": f.password})
	require.Equal(t, status, w.Code)
}

func TestIdentityEmailChangeIntegrationRequestVerifyAndLogin(t *testing.T) {
	// BE-IDN-020 §3 #1–3: two destinations, pending alias retained, then atomically replaced.
	f, capture, old, next := emailChangeFixture(t)
	emailChangeRequest(t, f, strings.ToUpper(next))
	require.True(t, *accountUser(t, f, f.user.ID).Email == old)
	emailChangeLogin(t, f, old, 200)
	emailChangeLogin(t, f, next, 401)
	emailChangeDeliver(t, f)
	require.Len(t, capture.messages, 2)
	require.True(t, capture.messages[0].Recipient == next)
	require.True(t, capture.messages[1].Recipient == old)
	link := capture.messages[0].Variables["Link"]
	require.True(t, strings.HasPrefix(link, f.accounts.PublicURL+"/auth/email/verify?tenant=default#token="))
	require.Contains(t, capture.messages[1].Body, "192.0.2.8")
	require.Contains(t, capture.messages[1].Body, f.now.UTC().Format(time.RFC3339))
	require.Contains(t, capture.messages[1].Body, "立即修改密码")
	require.Empty(t, capture.messages[1].Variables["Link"])
	raw := inviteRaw(t, link)
	row, found, err := f.accounts.Store.TokenByHash(f.ctx, 1, "email_change", refresh.Hash(raw))
	require.NoError(t, err)
	require.True(t, found)
	require.True(t, row.TokenHash == refresh.Hash(raw) && row.TokenHash != raw)
	require.Equal(t, 24*time.Hour, row.ExpiresAt.Sub(row.CreatedAt))
	emailChangeVerify(t, f, raw, 200)
	require.True(t, profileFields(t, f, f.token)["email"] == next)
	emailChangeLogin(t, f, old, 401)
	emailChangeLogin(t, f, next, 200)
	emailChangeVerify(t, f, raw, 400)
	emailChangePrivacy(t, f, old, next, raw)
}

func emailChangePrivacy(t *testing.T, f *accountFixture, values ...string) {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	for _, table := range []string{"audit_log", "notify_delivery", "notify_outbox"} {
		var rows []string
		require.NoError(t, q.Raw("SELECT row_to_json(r)::text FROM "+table+" r").Scan(&rows).Error)
		for _, row := range rows {
			for _, value := range values {
				require.False(t, strings.Contains(row, value), table+" must contain no private values")
			}
		}
	}
	var count int64
	require.NoError(t, q.Table("audit_log").Where("action IN ?", []string{"identity.email.change.request", "identity.email.change.verify"}).Count(&count).Error)
	require.EqualValues(t, 2, count)
}

func TestIdentityEmailChangeIntegrationReplacedAndExpired(t *testing.T) {
	// BE-IDN-020 §3 #4: latest application wins; the 24-hour boundary is exclusive.
	f, capture, old, next := emailChangeFixture(t)
	emailChangeRequest(t, f, next)
	emailChangeDeliver(t, f)
	first := inviteRaw(t, capture.messages[0].Variables["Link"])
	emailChangeRequest(t, f, next)
	emailChangeVerify(t, f, first, 400)
	emailChangeDeliver(t, f)
	second := inviteRaw(t, capture.messages[2].Variables["Link"])
	f.now = f.now.Add(24 * time.Hour)
	emailChangeVerify(t, f, second, 400)
	require.True(t, *accountUser(t, f, f.user.ID).Email == old)
}

func TestIdentityEmailChangeIntegrationValidationAndProfileBypass(t *testing.T) {
	// BE-IDN-020 §3 #5–6: failures enqueue nothing, and PUT profile cannot bypass proof.
	f, capture, old, occupied := emailChangeFixture(t)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	other := model.User{TenantID: 1, Username: "email-owner", PasswordHash: f.user.PasswordHash, DepartmentID: f.user.DepartmentID, Status: "active", Email: &occupied}
	require.NoError(t, q.Create(&other).Error)
	for _, tc := range []struct {
		email  string
		status int
	}{{old, 400}, {strings.ToUpper(old), 400}, {occupied, 409}, {"bad", 400}, {"", 400}} {
		w := f.request(t, "POST", "/auth/profile/email/change", f.token, map[string]any{"email": tc.email})
		require.Equal(t, tc.status, w.Code)
	}
	w := f.request(t, "PUT", "/auth/profile", f.token, map[string]any{"email": occupied})
	require.Equal(t, 400, w.Code)
	w = f.request(t, "PUT", "/auth/profile", f.token, map[string]any{"email": ""})
	require.Equal(t, 400, w.Code)
	w = f.request(t, "PUT", "/auth/profile", f.token, map[string]any{"email": old, "nickname": "retained"})
	require.Equal(t, 200, w.Code)
	require.True(t, *accountUser(t, f, f.user.ID).Email == old)
	require.Empty(t, capture.messages)
	var count int64
	require.NoError(t, q.Table("notify_outbox").Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, q.Table("auth_token").Where("purpose='email_change'").Count(&count).Error)
	require.Zero(t, count)
}

func TestIdentityEmailChangeIntegrationConflictDuringVerification(t *testing.T) {
	// BE-IDN-020: recheck occupancy at consumption; failed updates preserve the token.
	f, capture, old, next := emailChangeFixture(t)
	emailChangeRequest(t, f, next)
	emailChangeDeliver(t, f)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	other := model.User{TenantID: 1, Username: "late-owner", PasswordHash: f.user.PasswordHash, DepartmentID: f.user.DepartmentID, Status: "active", Email: &next}
	require.NoError(t, q.Create(&other).Error)
	raw := inviteRaw(t, capture.messages[0].Variables["Link"])
	emailChangeVerify(t, f, raw, 409)
	require.True(t, *accountUser(t, f, f.user.ID).Email == old)
	row, found, err := f.accounts.Store.TokenByHash(f.ctx, 1, "email_change", refresh.Hash(raw))
	require.NoError(t, err)
	require.True(t, found && row.Valid(f.now))
	encoded, err := json.Marshal(row)
	require.NoError(t, err)
	require.False(t, strings.Contains(string(encoded), next))
}
