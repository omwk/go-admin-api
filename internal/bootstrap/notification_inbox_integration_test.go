package bootstrap

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/crypto"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/stretchr/testify/require"
)

type s3aFixture struct {
	*adminFixture
	notifications *credentialNotifications
}

func newS3aFixture(t *testing.T) *s3aFixture {
	t.Helper()
	f := newAdminFixture(t)
	sealer, err := crypto.NewKeyring(map[int][]byte{1: f.secret}, 1)
	require.NoError(t, err)
	r := &credentialRefreshRuntime{database: f.database, sealer: sealer, now: func() time.Time { return f.now }}
	profiles, err := identity.NewProfileService(newProfileStore(f.database))
	require.NoError(t, err)
	require.NoError(t, registerCredentialNotificationRoutes(f.router, f.identityFixture.service, profiles, r))
	return &s3aFixture{adminFixture: f, notifications: newCredentialNotifications(r)}
}

func s3aRequest(t *testing.T, f *s3aFixture, method, path, key string, value any) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(value)
	require.NoError(t, err)
	r := httptest.NewRequest(method, "/api/v1/admin"+path, bytes.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+f.bearer)
	r.Header.Set("Content-Type", "application/json")
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, r)
	s3aValidateHTTP(t, method, path, w)
	return w
}

func s3aData(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var envelope struct {
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
	return envelope.Data
}

func TestSystemNotificationInboxIntegrationS3a(t *testing.T) {
	// S3a §4.1-4: count, read-all, filtering and recipient isolation share one scope.
	f := newS3aFixture(t)
	user := f.actor.User.ID
	require.NoError(t, f.database.Exec(f.ctx, `INSERT INTO notification(tenant_id,channel,target,template,payload,status)
 VALUES(1,'inbox','user:'||?::bigint::text,'notification.default','{}','sent'),
 (1,'inbox','user:'||?::bigint::text,'notification.default','{}','sent'),
 (1,'inbox','user:'||?::bigint::text,'credential.expired','{}','sent'),
 (1,'inbox','user:'||?::bigint::text,'notification.default','{}','sent'),
 (1,'inbox','user:'||?::bigint::text,'notification.default','{}','sent'),
 (1,'email','user:'||?::bigint::text,'notification.default','{}','pending')`, user, user, user, f.user.ID, f.user.ID, user))
	require.NoError(t, f.database.Exec(f.ctx, `INSERT INTO notification(tenant_id,channel,target,template,payload,status,read_at)
 VALUES(1,'inbox','user:'||?::bigint::text,'notification.default','{}','sent',now())`, user))
	require.NoError(t, f.database.Exec(f.ctx, `INSERT INTO notification(tenant_id,channel,target,template,payload,status)
 VALUES(2,'inbox','user:'||?::bigint::text,'notification.default','{}','sent')`, user))
	get := func(path string) map[string]any { return s3aData(t, s3aRequest(t, f, http.MethodGet, path, "", nil)) }
	require.EqualValues(t, 3, get("/notifications/unread-count")["count"])
	filtered := get("/notifications?unread=true&template=notification.default")
	require.EqualValues(t, 2, filtered["total"])
	require.Len(t, filtered["list"], 2)
	require.EqualValues(t, 4, get("/notifications?target=user:0&user_id=0&tenant_id=2")["total"])
	require.EqualValues(t, 1, get("/notifications?unread=false")["total"])
	for _, query := range []string{"unread=invalid", "unread=", "channel=unknown"} {
		w := s3aRequest(t, f, http.MethodGet, "/notifications?"+query, "", nil)
		require.Equal(t, 400, w.Code)
	}
	for run := 0; run < 2; run++ {
		w := s3aRequest(t, f, http.MethodPost, "/notifications/read-all", "read-all", nil)
		require.EqualValues(t, 3, s3aData(t, w)["updated"])
	}
	require.EqualValues(t, 0, get("/notifications/unread-count")["count"])
	require.EqualValues(t, 0, s3aData(t, s3aRequest(t, f, http.MethodPost, "/notifications/read-all", "", nil))["updated"])
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var others int64
	require.NoError(t, q.Table("notification").Where("target=? AND channel='inbox' AND read_at IS NULL", "user:"+fmtNotificationID(f.user.ID)).Count(&others).Error)
	require.EqualValues(t, 2, others)
	var excluded int64
	require.NoError(t, q.Table("notification").Where("(tenant_id=2 OR channel='email') AND read_at IS NULL").Count(&excluded).Error)
	require.EqualValues(t, 2, excluded, "read-all must preserve other tenants and external channels")
	w := s3aRequest(t, f, http.MethodPut, "/notifications/4/read", "", nil)
	require.Equal(t, 404, w.Code)
	t.Log("unread=3; filtered=2; read-all updated=3 (replay=3); unread=0; other user's unread=2; foreign read=404")
}

func TestSystemNotificationMenusIntegrationS3a(t *testing.T) {
	// S3a §4.9: both pages are siblings under /system and seed remains idempotent.
	f := newS3aFixture(t)
	require.NoError(t, SeedIdentity(f.ctx, f.database, f.password))
	w, navigation := profileRequest(t, f.router, "/api/v1/auth/menus", f.bearer)
	require.Equal(t, http.StatusOK, w.Code)
	system, found := s1bNavigationNode(navigation.Menus, "/system")
	require.True(t, found)
	for _, path := range []string{"/system/notifications", "/system/notifications/channels"} {
		page, found := s1bNavigationNode(system.Children, path)
		require.True(t, found, path)
		require.Equal(t, system.ID, page.ParentID)
		require.Equal(t, "menu", page.Type)
		require.NotNil(t, page.PermissionCode)
		require.True(t, strings.HasPrefix(*page.PermissionCode, "system:notify:"))
	}
	t.Log("/auth/menus: notification center and channels both project under /system")
}
