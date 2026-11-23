package main

import (
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/gateway"
	"github.com/stretchr/testify/require"
)

type gatewaySeedRow struct {
	gateway.Route
	CreatedAt, UpdatedAt time.Time
}

func TestSeedCLIIntegrationGatewayRouteIdempotency(t *testing.T) {
	// BE-IDN-030 §3.4: the real CLI seeds a fresh database and preserves edits.
	t.Setenv("CH_TENANT_PLATFORM_ID", "1")
	f := newSeedFixture(t)
	rows := gatewaySeedRows(t, f)
	require.Len(t, rows, 1)
	route := rows[0].Route
	require.Positive(t, route.ID)
	require.Equal(t, gateway.Route{ID: route.ID, TenantID: 1, Code: "usage.read", Name: "用量查询",
		Capability: "usage.read", Method: "GET", Path: "/open/v1/usage", RouteGroup: "usage",
		Visibility: "public", Status: "enabled", BillingMode: "free", UnitsPerCall: 1, Currency: "CNY", Version: 1}, route)
	f.seed(t)
	require.Equal(t, rows, gatewaySeedRows(t, f))
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.Table("api_route").Where("id=?", route.ID).Updates(map[string]any{
		"name": "tenant-custom-usage", "visibility": "private", "status": "disabled",
		"billing_mode": "per_call", "price_cents": 7, "units_per_call": 2, "version": 4,
		"created_by": f.admin.ID, "updated_at": time.Now().UTC().Truncate(time.Microsecond),
	}).Error)
	edited := gatewaySeedRows(t, f)
	f.seed(t)
	require.Equal(t, edited, gatewaySeedRows(t, f), "every persisted field must survive a repeated seed")
	t.Log("fresh CLI seed: usage.read GET /open/v1/usage public/free/enabled; repeated seed rows=1; tenant edits and timestamps unchanged")
}

func TestSeedCLIIntegrationGatewayRouteConfiguredTenant(t *testing.T) {
	// BE-IDN-030: the platform tenant comes from config, not an assumed tenant ID.
	t.Setenv("CH_TENANT_PLATFORM_ID", "1")
	f := newSeedFixture(t)
	before := gatewaySeedRows(t, f)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.Exec("INSERT INTO tenant(id,code,name) OVERRIDING SYSTEM VALUE VALUES(23,'gateway-platform','gateway-platform')").Error)
	t.Setenv("CH_TENANT_PLATFORM_ID", "23")
	f.seed(t)
	rows := gatewaySeedRows(t, f)
	require.Len(t, rows, 2)
	require.Equal(t, before[0], rows[0], "the previous platform tenant must remain untouched")
	require.EqualValues(t, 23, rows[1].TenantID)
	require.Equal(t, "usage.read", rows[1].Code)
	f.seed(t)
	require.Equal(t, rows, gatewaySeedRows(t, f))
	t.Log("CH_TENANT_PLATFORM_ID=23: usage.read seeded once in tenant 23; tenant 1 row preserved")
}

func gatewaySeedRows(t *testing.T, f *seedFixture) []gatewaySeedRow {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var rows []gatewaySeedRow
	require.NoError(t, q.Table("api_route").Select("id,tenant_id,created_by,code,name,capability,method,path,route_group,visibility,status,billing_mode,price_cents,units_per_call,currency,version,created_at,updated_at").Order("tenant_id,id").Find(&rows).Error)
	return rows
}
