package bootstrap

import (
	"encoding/json"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/stretchr/testify/require"
)

func TestSystemAuditIntegrationPasswordMinimumTwelve(t *testing.T) {
	// S4 §5.13: change the actual tenant setting, then reject an actual eight-character password.
	f, _ := newSystemAccountFixture(t)
	profiles, err := identity.NewProfileService(newProfileStore(f.database))
	require.NoError(t, err)
	require.NoError(t, identity.RegisterAdminRoutes(f.router, f.service, profiles, f.accounts.Admin))
	eight := accountPassword(t)[:8]
	create := identity.CreateUserRequest{Username: "s4_eight_baseline", Password: eight,
		UserFields: identity.UserFields{DepartmentID: f.user.DepartmentID, Status: "active"}}
	response := f.request(t, "POST", "/admin/users", f.token, create)
	require.Equal(t, 201, response.Code)
	response = f.request(t, "PUT", "/admin/settings/security", f.token, map[string]any{"password_min_length": 12})
	require.Equal(t, 200, response.Code)
	var envelope adminEnvelope
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
	require.EqualValues(t, 12, systemGroupValue(t, envelope.Data, "password_min_length"))
	create.Username = "s4_eight_rejected"
	response = f.request(t, "POST", "/admin/users", f.token, create)
	require.Equal(t, 400, response.Code)
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
	require.Equal(t, 10001, envelope.Code)
	response = f.request(t, "PUT", "/auth/password", f.token, map[string]any{"oldPassword": f.password, "newPassword": eight})
	require.Equal(t, 400, response.Code)
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
	require.Equal(t, 10001, envelope.Code)
	_, err = f.service.Login(f.ctx, 1, f.user.Username, f.password, identity.RequestMeta{})
	require.NoError(t, err, "the rejected password change must preserve the old credential")
	twelve := accountPassword(t)[:12]
	response = f.request(t, "PUT", "/auth/password", f.token, map[string]any{"oldPassword": f.password, "newPassword": twelve})
	require.Equal(t, 204, response.Code)
	_, err = f.service.Login(f.ctx, 1, f.user.Username, twelve, identity.RequestMeta{})
	require.NoError(t, err)
	t.Log("S4 #13: minimum=8/create(8)=201; PUT minimum=12=200; create(8)=400/10001; change(8)=400/10001; change(12)=204; login(12)=success")
}
