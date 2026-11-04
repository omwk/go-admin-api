package bootstrap

import (
	"encoding/json"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	syncmodule "github.com/omwk/go-admin-api/internal/modules/sync"
	"github.com/stretchr/testify/require"
)

func TestReplayIntegrationHTTPIdempotencyAndScope(t *testing.T) {
	f := replayFixture(t, 1)
	request := replayRequest(f)
	first := idemShopRequest(t, f.router, "POST", "/replays", request, f.bearer, "historical-once")
	require.Equal(t, 201, first.Code, first.Body.String())
	second := idemShopRequest(t, f.router, "POST", "/replays", request, f.bearer, "historical-once")
	require.Equal(t, 201, second.Code, second.Body.String())
	var a, b struct {
		Data syncmodule.ReplayRun `json:"data"`
	}
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &a))
	require.NoError(t, json.Unmarshal(second.Body.Bytes(), &b))
	require.Equal(t, a.Data, b.Data)
	rawCount(f.ctx, t, f.database, "replay_run", 1)
	rawCount(f.ctx, t, f.database, "replay_run_item", 1)
	request.Scope.Type = "quarantine"
	request.Scope.From = nil
	request.Scope.To = nil
	mismatch := idemShopRequest(t, f.router, "POST", "/replays", request, f.bearer, "historical-once")
	require.Equal(t, 409, mismatch.Code, mismatch.Body.String())
	var envelope struct {
		Code int `json:"code"`
	}
	require.NoError(t, json.Unmarshal(mismatch.Body.Bytes(), &envelope))
	require.Equal(t, 10006, envelope.Code)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	other := model.User{TenantID: 1, Username: "replay-denied", PasswordHash: f.identityFixture.user.PasswordHash, DepartmentID: f.identityFixture.user.DepartmentID, Status: "active"}
	require.NoError(t, q.Create(&other).Error)
	role := model.Role{TenantID: 1, Code: "replay-self", Name: "Replay scope fixture", DataScope: "self", Status: "active"}
	require.NoError(t, q.Create(&role).Error)
	require.NoError(t, q.Create(&model.UserRole{TenantID: 1, UserID: other.ID, RoleID: role.ID}).Error)
	denied, err := newAdminStore(f.database).Authorize(f.ctx, 1, other.ID)
	require.NoError(t, err)
	_, err = f.engineStore.ReadReplay(denied, a.Data.ID)
	require.Equal(t, 20107, errs.From(err).Code)
	require.NoError(t, q.Exec(`INSERT INTO sys_role_permission(tenant_id,role_id,permission_id) SELECT 1,?,id FROM sys_permission WHERE tenant_id=1 AND code='sync:replay:read'`, role.ID).Error)
	_, err = f.engineStore.ReadReplay(denied, a.Data.ID)
	require.Equal(t, 60101, errs.From(err).Code)
	_, err = f.engineStore.CreateReplay(denied, replayRequest(f), nil)
	require.Error(t, err)
	// The authenticated route also enforces the newly registered permission.
	profiles, err := identity.NewProfileService(newProfileStore(f.database))
	require.NoError(t, err)
	profile, err := profiles.Me(f.ctx, 1, other.ID)
	require.NoError(t, err)
	require.NotContains(t, profile.Permissions, "sync:replay:run")
	tokens, err := f.identityFixture.service.Login(f.ctx, 1, other.Username, f.identityFixture.password, identity.RequestMeta{})
	require.NoError(t, err)
	response := idemShopRequest(t, f.router, "POST", "/replays", replayRequest(f), tokens.AccessToken, "")
	require.Equal(t, 403, response.Code, response.Body.String())
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
	require.Equal(t, 20107, envelope.Code)
}
