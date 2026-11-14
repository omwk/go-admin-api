package bootstrap

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/notifier"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/scope"
	"github.com/omwk/go-admin-api/internal/modules/shop"
	shopmodel "github.com/omwk/go-admin-api/internal/modules/shop/model"
	"github.com/stretchr/testify/require"
)

func TestIdentitySuperScopeIntegrationRolelessInboxAndShops(t *testing.T) {
	// BE-IDN-006 §4 #1/#2/#6/#7/#9: current super facts grant tenant-wide scope without roles.
	f := newCredentialFixture(t)
	credential, _ := f.create(t, "oauth2")
	profiles, err := identity.NewProfileService(newProfileStore(f.database))
	require.NoError(t, err)
	runtime := &credentialRefreshRuntime{database: f.database}
	require.NoError(t, registerCredentialNotificationRoutes(f.router, f.identityFixture.service, profiles, runtime))
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.Where("tenant_id=1 AND user_id=?", f.actor.UserID).Delete(&model.UserRole{}).Error)
	require.NoError(t, q.Model(&shopmodel.Shop{}).Where("id=?", f.shop.ID).Update("owner_user_id", f.user.ID).Error)
	superScopeInboxRows(t, f, credential.ID)
	for _, path := range []string{"/notifications", "/notifications/unread-count"} {
		w, out := f.request(t, http.MethodGet, path, nil)
		require.Equal(t, http.StatusOK, w.Code)
		var data map[string]any
		require.NoError(t, json.Unmarshal(out.Data, &data))
		if path == "/notifications" {
			require.EqualValues(t, 2, data["total"])
			require.Len(t, data["list"], 2)
		} else {
			require.EqualValues(t, 2, data["count"])
		}
	}
	ctx, err := f.adminFixture.store.Authorize(f.ctx, 1, f.actor.UserID)
	require.NoError(t, err)
	access, err := scope.From(ctx)
	require.NoError(t, err)
	require.Equal(t, []scope.Role{{Kind: "all"}}, access.Roles)
	w, out := f.request(t, http.MethodGet, "/shops", nil)
	require.Equal(t, http.StatusOK, w.Code)
	var page shop.Page
	require.NoError(t, json.Unmarshal(out.Data, &page))
	require.EqualValues(t, 1, page.Total)
	require.Equal(t, f.shop.ID, page.List[0].ID)
	require.Empty(t, superScopeRoleCodes(t, f.identityFixture, 1, f.actor.UserID))
	require.NoError(t, q.Where("id=?", f.shop.ID).Delete(&shopmodel.Shop{}).Error)
	w, _ = f.request(t, http.MethodGet, f.shopPath(), nil)
	require.Equal(t, http.StatusNotFound, w.Code)
	t.Log("BE-IDN-006 #1/#2/#6/#9: roleless super inbox=200/total=2; unread=200/count=2; shops=200/total=1; deleted shop=404; stored roles remain empty")
}

func superScopeInboxRows(t *testing.T, f *credentialFixture, credential int64) {
	t.Helper()
	rows := []credentialNotification{}
	for _, recipient := range []int64{f.actor.UserID, f.user.ID} {
		for _, linked := range []bool{false, true} {
			row := credentialNotification{TenantID: 1, Target: "user:" + strconv.FormatInt(recipient, 10), Channel: "inbox",
				Template: "notification.default", Payload: json.RawMessage(`{}`), Status: "sent", CreatedAt: f.now, AvailableAt: f.now}
			if linked {
				row.ShopID, row.CredentialID = &f.shop.ID, &credential
			}
			rows = append(rows, row)
		}
	}
	foreign := rows[0]
	foreign.TenantID = 2
	rows = append(rows, foreign)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.Table("notification").Create(&rows).Error)
}

func TestIdentitySuperScopeIntegrationOrdinaryUserStillDenied(t *testing.T) {
	// BE-IDN-006 §4 #7: an ordinary roleless user sees personal notifications only.
	f := newCredentialFixture(t)
	credential, _ := f.create(t, "oauth2")
	profiles, err := identity.NewProfileService(newProfileStore(f.database))
	require.NoError(t, err)
	require.NoError(t, registerCredentialNotificationRoutes(f.router, f.identityFixture.service, profiles, &credentialRefreshRuntime{database: f.database}))
	superScopeInboxRows(t, f, credential.ID)
	_, err = f.adminFixture.store.Authorize(f.ctx, 1, f.user.ID)
	require.Equal(t, errs.ErrDataScopeDenied, errs.From(err).Code)
	f.bearer = f.login(t).AccessToken
	w, _ := f.request(t, http.MethodGet, "/notifications", nil)
	require.Equal(t, http.StatusForbidden, w.Code)
	result, err := newCredentialNotifications(&credentialRefreshRuntime{database: f.database}).Inbox(f.ctx, 1, f.user.ID, 1, 20, notifier.InboxFilter{})
	require.NoError(t, err)
	data, err := json.Marshal(result)
	require.NoError(t, err)
	var inbox map[string]any
	require.NoError(t, json.Unmarshal(data, &inbox))
	require.EqualValues(t, 1, inbox["total"])
	w, _ = f.request(t, http.MethodGet, "/notifications/unread-count", nil)
	require.Equal(t, http.StatusForbidden, w.Code)
	w, out := f.request(t, http.MethodGet, "/shops", nil)
	require.Equal(t, http.StatusForbidden, w.Code)
	require.Equal(t, errs.ErrPermissionDenied, out.Code)
	t.Log("BE-IDN-006 #7: ordinary roleless Authorize=20108; inbox query excludes shop notifications; inbox/unread/shops HTTP=403")
}

func superScopeRoleCodes(t *testing.T, f *identityFixture, tenant, user int64) []string {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	codes := []string{}
	require.NoError(t, q.Table("sys_user_role ur").Joins("JOIN sys_role r ON r.tenant_id=ur.tenant_id AND r.id=ur.role_id").
		Where("ur.tenant_id=? AND ur.user_id=?", tenant, user).Order("r.code").Pluck("r.code", &codes).Error)
	return codes
}

func ordinaryScopeActor(t *testing.T, f *identityFixture, user int64) {
	t.Helper()
	// Role restrictions apply to ordinary administrators; super users have all scope.
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	result := q.Model(&model.User{}).Where("tenant_id=? AND id=?", f.user.TenantID, user).Update("is_super", false)
	require.NoError(t, result.Error)
	require.EqualValues(t, 1, result.RowsAffected)
}

func TestIdentitySuperScopeIntegrationTenantAndStatusGuards(t *testing.T) {
	// BE-IDN-006: all scope preserves the verified account, live tenant and tenant boundary checks.
	f := newCredentialFixture(t)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	tenant := model.Tenant{Code: "super-scope-foreign", Name: "foreign", Status: "active", Settings: json.RawMessage(`{}`)}
	require.NoError(t, q.Create(&tenant).Error)
	dept := model.Department{TenantID: tenant.ID, Name: "foreign", Path: "1", Status: "active"}
	require.NoError(t, q.Create(&dept).Error)
	user := model.User{TenantID: tenant.ID, Username: "foreign-super", DepartmentID: dept.ID,
		PasswordHash: f.user.PasswordHash, IsSuper: true, Status: "active"}
	require.NoError(t, q.Create(&user).Error)
	ctx, err := f.adminFixture.store.Authorize(f.ctx, tenant.ID, user.ID)
	require.NoError(t, err)
	_, err = f.store.Shop(ctx, tenant.ID, f.shop.ID)
	require.Equal(t, errs.ErrShopNotFound, errs.From(err).Code)
	_, err = f.adminFixture.store.Authorize(f.ctx, 1, user.ID)
	require.Error(t, err)
	for _, status := range []string{"disabled", "locked"} {
		require.NoError(t, q.Model(&model.User{}).Where("id=?", user.ID).Update("status", status).Error)
		_, err = f.adminFixture.store.Authorize(f.ctx, tenant.ID, user.ID)
		require.Error(t, err)
	}
	require.NoError(t, q.Model(&model.User{}).Where("id=?", user.ID).Update("status", "active").Error)
	require.NoError(t, q.Model(&model.Tenant{}).Where("id=?", tenant.ID).Update("status", "suspended").Error)
	_, err = f.adminFixture.store.Authorize(f.ctx, tenant.ID, user.ID)
	require.Error(t, err)
	t.Log("roleless super: foreign shop=404; mismatched tenant, disabled/locked account and suspended tenant all rejected")
}
