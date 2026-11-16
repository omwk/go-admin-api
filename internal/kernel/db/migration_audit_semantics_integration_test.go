package db

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMigrateAuditSemanticsIntegrationBackfillAndAppendOnly(t *testing.T) {
	// BE-IDN-009 §4.8–9/17: isolated v30 history is upgraded without changing its count.
	database, ctx := isolatedDB(t, true)
	state, err := database.Migrate(ctx, "down")
	require.NoError(t, err)
	require.EqualValues(t, 30, state.Version)
	q, cancel := database.WithContext(ctx)
	defer cancel()
	var dept, user int64
	require.NoError(t, q.Raw("INSERT INTO sys_dept(tenant_id,name,path,status) VALUES(1,'Historical department','1','active') RETURNING id").Scan(&dept).Error)
	require.NoError(t, q.Raw("INSERT INTO sys_user(tenant_id,username,password_hash,dept_id,status) VALUES(1,'historical-admin','***',?,'active') RETURNING id", dept).Scan(&user).Error)
	require.NoError(t, database.Exec(ctx, `INSERT INTO audit_log(tenant_id,actor_type,actor_id,action,resource_type,resource_id,trace_id,result)
		SELECT 1,'user',?,'system:dept:update','dept',?,'historical-backfill','success' FROM generate_series(1,24)`, user, strconv.FormatInt(dept, 10)))
	require.NoError(t, database.Exec(ctx, `INSERT INTO audit_log(tenant_id,actor_type,actor_id,action,resource_type,resource_id,trace_id,result)
		VALUES(1,'user',999999,'system:dept:delete','dept','999999','historical-missing','success')`))
	var before int64
	require.NoError(t, q.Table("audit_log").Count(&before).Error)
	state, err = database.Migrate(ctx, "up")
	require.NoError(t, err)
	require.EqualValues(t, 31, state.Version)
	var stats struct{ Total, Actors, Tenants, Resources, Colons, Invalid int64 }
	require.NoError(t, q.Raw(`SELECT count(*) AS total,count(actor_name) AS actors,count(actor_tenant_name) AS tenants,count(resource_label) AS resources,
		count(*) FILTER (WHERE action LIKE '%:%') AS colons,count(*) FILTER (WHERE action !~ '^[a-z]+\.[a-z.]+$') AS invalid FROM audit_log`).Scan(&stats).Error)
	require.Equal(t, before, stats.Total)
	require.EqualValues(t, 24, stats.Actors)
	require.EqualValues(t, 25, stats.Tenants)
	require.EqualValues(t, 24, stats.Resources)
	require.Zero(t, stats.Colons)
	require.Zero(t, stats.Invalid)
	require.GreaterOrEqual(t, 100*stats.Actors/stats.Total, int64(90))
	require.GreaterOrEqual(t, 100*stats.Resources/stats.Total, int64(90))
	var names struct{ ActorName, ActorTenantName, ResourceLabel string }
	require.NoError(t, q.Table("audit_log").Select("actor_name,actor_tenant_name,resource_label").Where("actor_id=?", user).Take(&names).Error)
	require.Equal(t, "historical-admin", names.ActorName)
	require.Equal(t, "default", names.ActorTenantName)
	require.Equal(t, "Historical department", names.ResourceLabel)
	var missing struct{ ActorName, ResourceLabel *string }
	require.NoError(t, q.Table("audit_log").Select("actor_name,resource_label").Where("actor_id=999999").Take(&missing).Error)
	require.Nil(t, missing.ActorName)
	require.Nil(t, missing.ResourceLabel)
	require.Error(t, database.Exec(ctx, "UPDATE audit_log SET action='identity.login'"))
	require.Error(t, database.Exec(ctx, "DELETE FROM audit_log"))
	require.Error(t, database.Exec(ctx, "TRUNCATE audit_log"))
	t.Logf("historical migration: before=%d after=%d colon=%d invalid=%d; actor_name=%d/%d (%.1f%%), actor_tenant_name=%d/%d (100%%), resource_label=%d/%d (%.1f%%); unresolved names=NULL",
		before, stats.Total, stats.Colons, stats.Invalid, stats.Actors, stats.Total, 100*float64(stats.Actors)/float64(stats.Total), stats.Tenants, stats.Total, stats.Resources, stats.Total, 100*float64(stats.Resources)/float64(stats.Total))
}
