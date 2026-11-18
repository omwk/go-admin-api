package bootstrap

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/refresh"
	"github.com/stretchr/testify/require"
)

func TestIdentityAccountTenantCodeIntegrationInviteAndReset(t *testing.T) {
	f := newAccountFixture(t)
	tenant, owner, dept := accountCodeTenant(t, f)
	for index, tc := range []struct {
		name  string
		hint  map[string]any
		valid bool
	}{
		{"code only", map[string]any{"tenant": tenant.Code}, true},
		{"code wins over conflicting ID", map[string]any{"tenant": tenant.Code, "tenant_id": 1}, true},
		{"trimmed code", map[string]any{"tenant": " " + tenant.Code + " "}, true},
		{"legacy ID", map[string]any{"tenant_id": tenant.ID}, true},
		{"blank code falls back", map[string]any{"tenant": " ", "tenant_id": tenant.ID}, true},
		{"unknown code never falls back", map[string]any{"tenant": "missing", "tenant_id": tenant.ID}, false},
		{"code is case sensitive", map[string]any{"tenant": "ACME", "tenant_id": tenant.ID}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f.server.FlushAll()
			invite := accountCodeToken(t, f, tenant.ID, owner.ID, &dept.ID, "invite")
			input := map[string]any{"token": invite, "username": fmt.Sprintf("code-member-%d", index), "password": accountPassword(t)}
			for key, value := range tc.hint {
				input[key] = value
			}
			accepted := f.request(t, "POST", "/auth/invite/accept", "", input)
			want := 400
			if tc.valid {
				want = 201
			}
			require.Equal(t, want, accepted.Code, accepted.Body.String())
			if !tc.valid {
				require.Contains(t, accepted.Body.String(), "邀请或链接无效")
				row, found, err := f.accounts.Store.TokenByHash(f.ctx, tenant.ID, "invite", refresh.Hash(invite))
				require.NoError(t, err)
				require.True(t, found)
				require.Zero(t, row.UsedCount)
			}
			reset := accountCodeToken(t, f, tenant.ID, owner.ID, nil, "reset")
			input = map[string]any{"token": reset, "newPassword": accountPassword(t)}
			for key, value := range tc.hint {
				input[key] = value
			}
			result := f.request(t, "POST", "/auth/password/reset", "", input)
			if tc.valid {
				want = 204
			}
			require.Equal(t, want, result.Code, result.Body.String())
			if !tc.valid {
				require.Contains(t, result.Body.String(), "邀请或链接无效")
			}
		})
	}
}

func accountCodeTenant(t *testing.T, f *accountFixture) (model.Tenant, model.User, model.Department) {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	tenant := model.Tenant{Code: "acme", Name: "account-code", Status: "active", Settings: json.RawMessage(`{}`)}
	require.NoError(t, q.Create(&tenant).Error)
	dept := model.Department{TenantID: tenant.ID, Name: "root", Path: "1", Status: "active"}
	require.NoError(t, q.Create(&dept).Error)
	owner := model.User{TenantID: tenant.ID, Username: "code-owner", DepartmentID: dept.ID, PasswordHash: f.user.PasswordHash, IsSuper: true, Status: "active"}
	require.NoError(t, q.Create(&owner).Error)
	return tenant, owner, dept
}

func accountCodeToken(t *testing.T, f *accountFixture, tenant, owner int64, dept *int64, purpose string) string {
	t.Helper()
	raw, err := refresh.Generate()
	require.NoError(t, err)
	row := model.AuthToken{TenantID: tenant, Purpose: purpose, TokenHash: refresh.Hash(raw), ExpiresAt: f.now.Add(time.Hour), CreatedBy: owner,
		DepartmentID: dept, RoleIDs: json.RawMessage(`[]`), MaxUses: 1}
	if purpose == "reset" {
		row.UserID = &owner
	}
	require.NoError(t, f.accounts.Store.InsertToken(f.ctx, &row))
	return raw
}
