package bootstrap

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/crypto"
	"github.com/omwk/go-admin-api/internal/kernel/httpx"
	"github.com/omwk/go-admin-api/internal/kernel/logger"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/shop"
	shopmodel "github.com/omwk/go-admin-api/internal/modules/shop/model"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	gormlog "gorm.io/gorm/logger"
)

type credentialLog struct {
	mu sync.Mutex
	bytes.Buffer
}

func (b *credentialLog) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.Write(p)
}

func (b *credentialLog) text() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.String()
}

type credentialFixture struct {
	*adminFixture
	service *shop.Service
	store   *shopStore
	sealer  crypto.Sealer
	shop    shopmodel.Shop
	actor   shop.Actor
	logs    *credentialLog
	sqlLogs *credentialLog
	markers []string
}

func newCredentialFixture(t *testing.T) *credentialFixture {
	t.Helper()
	admin := newAdminFixture(t)
	sealer, err := crypto.NewKeyring(map[int][]byte{1: shopRandomBytes(t, 32)}, 1)
	require.NoError(t, err)
	f := &credentialFixture{adminFixture: admin, sealer: sealer, store: newShopStore(admin.database), logs: &credentialLog{}, sqlLogs: &credentialLog{}}
	f.actor = shop.Actor{TenantID: 1, UserID: admin.actor.User.ID, DepartmentID: admin.actor.User.DepartmentID}
	sqlLogger := gormlog.New(log.New(f.sqlLogs, "", 0), gormlog.Config{LogLevel: gormlog.Info, ParameterizedQueries: true})
	f.store.raw = func(ctx context.Context) (*gorm.DB, context.CancelFunc) {
		q, cancel := f.database.WithContext(ctx)
		return q.Session(&gorm.Session{Logger: sqlLogger}), cancel
	}
	f.service, err = shop.NewService(f.store, admin.store.Authorize, sealer)
	require.NoError(t, err)
	f.useService(t, f.service)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var platform shopmodel.Platform
	require.NoError(t, q.Select("id").Where("code = ?", "shopee").Take(&platform).Error)
	f.shop = shopmodel.Shop{TenantID: 1, PlatformID: platform.ID, Name: "credential-parent", ExternalShopID: "credential-parent", Timezone: "UTC", Currency: "CNY", OwnerUserID: f.actor.UserID, DepartmentID: f.actor.DepartmentID}
	require.NoError(t, q.Create(&f.shop).Error)
	return f
}

func (f *credentialFixture) useService(t *testing.T, service *shop.Service) {
	t.Helper()
	appLog, err := logger.New(f.logs, "debug", "json")
	require.NoError(t, err)
	f.router, err = httpx.NewEngine(httpx.Dependencies{DB: f.database, Redis: f.redis, Log: appLog})
	require.NoError(t, err)
	profiles, err := identity.NewProfileService(newProfileStore(f.database))
	require.NoError(t, err)
	require.NoError(t, shop.RegisterRoutes(f.router, f.identityFixture.service, profiles, service))
	f.service = service
}

func (f *credentialFixture) randomValue(t *testing.T) string {
	t.Helper()
	marker := hex.EncodeToString(shopRandomBytes(t, 32))
	f.markers = append(f.markers, marker)
	return marker
}

func (f *credentialFixture) request(t *testing.T, method, path string, payload any) (*httptest.ResponseRecorder, shopHTTPEnvelope) {
	t.Helper()
	response, envelope := shopHTTPRequest(t, f.router, method, "/api/v1/admin"+path, payload, f.bearer)
	f.noLeaks(t, "response body", response.Body.String())
	headers, err := json.Marshal(response.Header())
	require.NoError(t, err)
	f.noLeaks(t, "response headers", string(headers))
	return response, envelope
}

func (f *credentialFixture) create(t *testing.T, kind string) (shop.CredentialView, map[string]string) {
	t.Helper()
	values := map[string]string{"private_value": f.randomValue(t), "user_agent": f.randomValue(t), "browser_fingerprint": f.randomValue(t)}
	response, envelope := f.request(t, http.MethodPost, f.shopPath()+"/credentials", map[string]any{
		"kind": kind, "label": "primary", "values": values, "extra": map[string]string{"scope": "product,order"},
	})
	require.Equal(t, 201, response.Code, response.Body.String())
	var view shop.CredentialView
	require.NoError(t, json.Unmarshal(envelope.Data, &view))
	var projection map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(envelope.Data, &projection))
	require.ElementsMatch(t, []string{"id", "kind", "label", "status", "fingerprint", "expires_at", "last_checked_at", "version_no", "extra"}, mapKeys(projection))
	return view, values
}

func mapKeys(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	return keys
}

func (f *credentialFixture) shopPath() string { return "/shops/" + strconv.FormatInt(f.shop.ID, 10) }
func credentialPath(id int64) string          { return "/credentials/" + strconv.FormatInt(id, 10) }

func (f *credentialFixture) noLeaks(t *testing.T, channel, value string) {
	t.Helper()
	for _, marker := range f.markers {
		require.False(t, bytes.Contains([]byte(value), []byte(marker)), "sensitive marker leaked through %s", channel)
	}
}

func (f *credentialFixture) readStored(t *testing.T, id int64, number int) (shopmodel.Credential, shopmodel.CredentialVersion) {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var row shopmodel.Credential
	var version shopmodel.CredentialVersion
	require.NoError(t, q.Select("*").Where("id = ?", id).Take(&row).Error)
	require.NoError(t, q.Select("*").Where("credential_id = ? AND version_no = ?", id, number).Take(&version).Error)
	return row, version
}

func (f *credentialFixture) databaseSnapshot(t *testing.T) string {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var rows []string
	err := q.Raw(`SELECT to_jsonb(c)::text FROM shop_credential c UNION ALL
SELECT to_jsonb(v)::text FROM credential_version v UNION ALL SELECT to_jsonb(a)::text FROM credential_audit a ORDER BY 1`).Scan(&rows).Error
	require.NoError(t, err)
	encoded, err := json.Marshal(rows)
	require.NoError(t, err)
	f.noLeaks(t, "database all columns", string(encoded))
	return string(encoded)
}
