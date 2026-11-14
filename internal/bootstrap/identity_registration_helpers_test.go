package bootstrap

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

type registrationFixture struct {
	*accountFixture
	doc contractMap
}

type registrationEnvelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func newRegistrationFixture(t *testing.T) *registrationFixture {
	t.Helper()
	f := newAccountFixture(t)
	profiles, err := identity.NewProfileService(newProfileStore(f.database))
	require.NoError(t, err)
	require.NoError(t, registerSystemRoutes(f.router, f.service, profiles, f.accounts.Admin, f.database))
	identity.RegisterAccountRegistrationRoutes(f.router, f.accounts)
	raw, err := os.ReadFile("../../api/openapi/identity.yaml")
	require.NoError(t, err)
	result := &registrationFixture{accountFixture: f}
	require.NoError(t, yaml.Unmarshal(raw, &result.doc))
	return result
}

func (f *registrationFixture) call(t *testing.T, method, path string, value any) (*httptest.ResponseRecorder, registrationEnvelope) {
	t.Helper()
	bearer := ""
	if strings.HasPrefix(path, "/admin/") {
		bearer = f.token
	}
	w := f.request(t, method, path, bearer, value)
	validateOpenAPIResponse(t, f.doc, "/api/v1"+path, strings.ToLower(method), w.Code, w.Body.Bytes())
	var envelope registrationEnvelope
	if w.Code != 204 {
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
	}
	return w, envelope
}

func (f *registrationFixture) settings(t *testing.T, values map[string]any) {
	t.Helper()
	w, out := f.call(t, "PUT", "/admin/settings/tenant", values)
	require.Equal(t, 200, w.Code, out.Code, out.Message)
}

func registrationInput(t *testing.T, account string) identity.RegisterInput {
	t.Helper()
	return identity.RegisterInput{Tenant: "default", Account: account, Password: accountPassword(t),
		Email: strings.ToLower(accountPassword(t)) + "@example.invalid"}
}

func (f *registrationFixture) register(t *testing.T, in identity.RegisterInput, status int) identity.RegistrationResult {
	t.Helper()
	w, out := f.call(t, "POST", "/auth/register", in)
	require.Equal(t, status, w.Code, out.Code, out.Message)
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	var result identity.RegistrationResult
	require.NoError(t, json.Unmarshal(out.Data, &result))
	require.True(t, result.Accepted)
	return result
}

func (f *registrationFixture) verify(t *testing.T, link string, status int) {
	t.Helper()
	w, out := f.call(t, "POST", "/auth/register/verify", map[string]any{"token": inviteRaw(t, link)})
	require.Equal(t, status, w.Code, out.Code, out.Message)
}

func registrationUser(t *testing.T, f *registrationFixture, tenant int64, account string) model.User {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var user model.User
	require.NoError(t, q.Select("id,tenant_id,username,email,status,is_super,dept_id,password_hash").Where("tenant_id=? AND username=?", tenant, account).Take(&user).Error)
	return user
}

func registrationCount(t *testing.T, f *registrationFixture, table string, tenant, user int64) int64 {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	query := q.Table(table).Where("tenant_id=?", tenant)
	if user > 0 {
		query = query.Where("user_id=?", user)
	}
	var count int64
	require.NoError(t, query.Count(&count).Error)
	return count
}

func registrationTenant(t *testing.T, f *registrationFixture, code string) int64 {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	row := model.Tenant{Code: code, Name: code, Status: "active", Settings: json.RawMessage(`{"allow_self_registration":true}`)}
	require.NoError(t, q.Create(&row).Error)
	root := model.Department{TenantID: row.ID, Name: "root", Path: "1", Status: "active"}
	require.NoError(t, q.Create(&root).Error)
	return row.ID
}
