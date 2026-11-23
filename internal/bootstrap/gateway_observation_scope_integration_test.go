package bootstrap

import (
	"fmt"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/omwk/go-admin-api/internal/modules/gateway"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
	"github.com/stretchr/testify/require"
)

func TestGatewayObservabilityIntegrationTenantAndPermissions(t *testing.T) {
	// BE-IDN-029 acceptance 10: only the configured platform super may cross tenants.
	f := newGatewayHTTPFixture(t)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.Exec("INSERT INTO tenant(id,code,name) OVERRIDING SYSTEM VALUE VALUES(2,'observation-foreign','observation-foreign') ON CONFLICT(id) DO NOTHING").Error)
	foreign := gateway.Client{TenantID: 2, AppID: ulid.Make().String(), Name: "foreign-client", OwnerUserID: f.gatewayActor().UserID, Status: "active", Version: 1, IPWhitelist: []string{}}
	require.NoError(t, f.gate.CreateClient(f.ctx, &foreign))
	observationLog(t, f, 2, foreign.ID, f.route.ID, f.now.Add(-time.Hour), 200, 10, "foreign-only")
	require.EqualValues(t, 1, observationPage(t, f, "?trace_id=foreign-only").Total)
	require.NoError(t, q.Table("sys_user").Where("id=?", f.gatewayActor().UserID).Update("is_super", false).Error)
	require.Zero(t, observationPage(t, f, "?trace_id=foreign-only").Total)
	for _, path := range []string{fmt.Sprintf("/api-calls?client_id=%d", foreign.ID), fmt.Sprintf("/api-calls/summary?group_by=client&client_id=%d", foreign.ID)} {
		w, _ := f.manage(t, "GET", path, nil, "")
		require.Equal(t, 404, w.Code, w.Body.String())
	}
	w, _ := f.manage(t, "POST", fmt.Sprintf("/api-clients/%d/try", foreign.ID), observationInput(f), "")
	require.Equal(t, 404, w.Code, w.Body.String())
	var count int64
	require.NoError(t, q.Table("api_call_log").Where("charge_source='manual_test'").Count(&count).Error)
	require.Zero(t, count, "foreign trial must be rejected before execution")

	// The two new permissions must be granted by the existing repeatable seed path.
	require.NoError(t, SeedIdentity(f.ctx, f.database, f.password))
	require.NoError(t, SeedIdentity(f.ctx, f.database, f.password))
	for _, code := range []string{permission.GatewayClientList, permission.GatewayClientCreate, permission.GatewayClientManage, permission.GatewayRouteList, permission.GatewayRouteCreate, permission.GatewayRouteUpdate, permission.GatewayCallRead} {
		require.NoError(t, permission.Registered(code))
		require.NoError(t, q.Table("sys_role_permission rp").Joins("JOIN sys_role r ON r.id=rp.role_id AND r.tenant_id=rp.tenant_id").Joins("JOIN sys_permission p ON p.id=rp.permission_id AND p.tenant_id=rp.tenant_id").Where("rp.tenant_id=1 AND r.code=? AND p.code=?", identity.SuperAdminRoleCode, code).Count(&count).Error)
		require.EqualValues(t, 1, count, code)
	}
	t.Log("#10 foreign logs hidden; foreign client filter/summary/trial=404; platform super can observe foreign logs; repeat seed grants all 7 required permissions once")
}
