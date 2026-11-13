package bootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/crypto"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/stretchr/testify/require"
)

type tenantFixture struct {
	*adminFixture
	profiles *identity.ProfileService
	tenants  *identity.TenantService
	doc      contractMap
}

func newTenantFixture(t *testing.T) *tenantFixture {
	t.Helper()
	f := &tenantFixture{adminFixture: newAdminFixture(t), doc: readSystemOpenAPI(t)}
	var err error
	f.profiles, err = identity.NewProfileService(newProfileStore(f.database))
	require.NoError(t, err)
	accounts := &identity.AccountService{Store: &accountStore{f.identityFixture.store}, Auth: f.identityFixture.service, Admin: f.service,
		Limits: f.redis, Now: func() time.Time { return f.now }, PublicURL: "https://accounts.example.invalid"}
	f.identityFixture.service.UseAccounts(accounts)
	f.service.UseAccounts(accounts)
	require.NoError(t, identity.RegisterInviteRoutes(f.router, f.identityFixture.service, f.profiles, accounts, 1))
	sealer, err := crypto.NewKeyring(map[int][]byte{1: f.secret}, 1)
	require.NoError(t, err)
	f.tenants, err = identity.NewTenantService(newTenantStore(f.database), f.service, 1, accounts.PublicURL, accounts.Now, sealer)
	require.NoError(t, err)
	require.NoError(t, identity.RegisterTenantRoutes(f.router, f.identityFixture.service, f.profiles, f.tenants))
	return f
}

func (f *tenantFixture) request(t *testing.T, method, path string, value any, key string) (*httptest.ResponseRecorder, adminEnvelope) {
	t.Helper()
	var body []byte
	if value != nil {
		var err error
		body, err = json.Marshal(value)
		require.NoError(t, err)
	}
	r := httptest.NewRequest(method, "/api/v1"+path, bytes.NewReader(body)).WithContext(f.ctx)
	r.Header.Set("Content-Type", "application/json")
	r.RemoteAddr = "192.0.2.9:1234"
	if f.bearer != "" {
		r.Header.Set("Authorization", "Bearer "+f.bearer)
	}
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, r)
	parts := strings.Split(strings.Split(path, "?")[0], "/")
	for i, part := range parts {
		if _, err := strconv.ParseInt(part, 10, 64); err == nil {
			parts[i] = "{id}"
		}
	}
	doc := f.doc
	if strings.HasPrefix(path, "/auth/") {
		doc = f.contract
	}
	if path == "/auth/invite/accept" {
		// The reused F1 endpoint has no route contract yet; verify its existing safe schema.
		schema := "ErrorResponse"
		if w.Code == 201 {
			schema = "SuccessEnvelope"
		}
		var body any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
		validateContract(t, f.contract, contractMap{"$ref": "#/components/schemas/" + schema}, body)
	} else {
		validateOpenAPIResponse(t, doc, "/api/v1"+strings.Join(parts, "/"), strings.ToLower(method), w.Code, w.Body.Bytes())
	}
	var out adminEnvelope
	if w.Code != 204 {
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	}
	return w, out
}

func (f *tenantFixture) template(t *testing.T, code string) model.TenantTemplate {
	t.Helper()
	w, out := f.request(t, "POST", "/admin/tenant-templates", identity.CreateTemplateInput{SourceTenantID: 1, Code: code, Name: code}, "")
	require.Equal(t, 201, w.Code)
	var row model.TenantTemplate
	require.NoError(t, json.Unmarshal(out.Data, &row))
	return row
}

func (f *tenantFixture) provision(t *testing.T, template int64, code string) identity.ProvisionTenantResult {
	t.Helper()
	w, out := f.request(t, "POST", "/admin/tenants", tenantProvisionInput(template, code), "")
	require.Equal(t, 201, w.Code)
	var result identity.ProvisionTenantResult
	require.NoError(t, json.Unmarshal(out.Data, &result))
	require.NotZero(t, result.TenantID)
	return result
}

func tenantProvisionInput(template int64, code string) identity.ProvisionTenantInput {
	return identity.ProvisionTenantInput{TemplateID: template, Code: code, Name: code, AdminUsername: code + "-admin"}
}

func (f *tenantFixture) accept(t *testing.T, result identity.ProvisionTenantResult, username string) (identity.Tokens, string) {
	t.Helper()
	password := accountPassword(t)
	w, _ := f.request(t, "POST", "/auth/invite/accept", map[string]any{"tenant_id": result.TenantID, "token": inviteRaw(t, result.InviteURL), "password": password}, "")
	require.Equal(t, 201, w.Code)
	w, out := f.request(t, "POST", "/auth/login", map[string]any{"tenant_id": result.TenantID, "account": username, "password": password}, "")
	require.Equal(t, 200, w.Code)
	var tokens identity.Tokens
	require.NoError(t, json.Unmarshal(out.Data, &tokens))
	return tokens, password
}

func tenantPath(id int64) string   { return "/admin/tenants/" + strconv.FormatInt(id, 10) }
func templatePath(id int64) string { return "/admin/tenant-templates/" + strconv.FormatInt(id, 10) }

func (f *tenantFixture) count(t *testing.T, table string, tenant int64) int64 {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var count int64
	require.NoError(t, q.Table(table).Where("tenant_id=?", tenant).Count(&count).Error)
	return count
}

func (f *tenantFixture) snapshot(t *testing.T, tenant int64) model.TenantSnapshot {
	t.Helper()
	var snap model.TenantSnapshot
	require.NoError(t, f.database.Transaction(f.ctx, func(ctx context.Context) error {
		var err error
		snap, err = newTenantStore(f.database).Snapshot(ctx, tenant)
		return err
	}))
	return snap
}

func canonicalTenantSnapshot(t *testing.T, snap model.TenantSnapshot) string {
	t.Helper()
	names := map[string]string{"": ""}
	for _, row := range snap.Menus {
		names[row.Key] = row.Name
	}
	for i := range snap.Menus {
		row := &snap.Menus[i]
		row.Key, row.ParentKey = names[row.Key], names[row.ParentKey]
		row.ID, row.TenantID, row.ParentID = 0, 0, 0
	}
	for i := range snap.Roles {
		row := &snap.Roles[i]
		for j, key := range row.Menus {
			row.Menus[j] = names[key]
		}
		sort.Strings(row.Menus)
		sort.Strings(row.Permissions)
	}
	sort.Slice(snap.Menus, func(i, j int) bool { return snap.Menus[i].Key < snap.Menus[j].Key })
	sort.Slice(snap.Roles, func(i, j int) bool { return snap.Roles[i].Code < snap.Roles[j].Code })
	data, err := json.Marshal(snap)
	require.NoError(t, err)
	return string(data)
}

func canonicalTenantNavigation(nodes []identity.MenuNode) []identity.MenuNode {
	result := make([]identity.MenuNode, len(nodes))
	for i, row := range nodes {
		row.ID, row.ParentID = 0, 0
		row.Children = canonicalTenantNavigation(row.Children)
		result[i] = row
	}
	return result
}
