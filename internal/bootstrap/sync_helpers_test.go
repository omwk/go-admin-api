package bootstrap

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/logger"
	"github.com/omwk/go-admin-api/internal/modules/connector"
	"github.com/omwk/go-admin-api/internal/modules/connector/platforms/mock"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/ingestion"
	syncmodule "github.com/omwk/go-admin-api/internal/modules/sync"
	"github.com/omwk/go-admin-api/internal/modules/sync/repo"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm/clause"
)

type syncFixture struct {
	*credentialFixture
	engine           *syncmodule.Orchestrator
	engineStore      *syncEngineStore
	platform         *mock.Platform
	jobID, mappingID int64
}

func syncDataset(t *testing.T, count int) []json.RawMessage {
	t.Helper()
	items := make([]json.RawMessage, count)
	for i := range items {
		raw, err := json.Marshal(map[string]any{"id": fmt.Sprintf("product-%03d", i+1), "title": "mock product", "price": 1234, "currency": "CNY", "status": "online", "stock": 10, "skus": []map[string]any{{"id": fmt.Sprintf("sku-%03d", i+1), "price": 1234, "stock": 10}}})
		require.NoError(t, err)
		items[i] = raw
	}
	return items
}

func newSyncFixture(t *testing.T, items []json.RawMessage) *syncFixture {
	t.Helper()
	f := &syncFixture{credentialFixture: newCredentialFixture(t), platform: mock.NewDataset(items)}
	f.create(t, "api_key")
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.Table("platform").Where("id=?", f.shop.PlatformID).Updates(map[string]any{"code": "mock", "capabilities": json.RawMessage(`{"resources":["product"],"incremental_modes":["page"]}`)}).Error)
	spec := ingestion.Spec{SchemaVersion: 1, Resource: "product", KeyField: "id", StatusTables: map[string]map[string]string{}}
	for target, source := range map[string]string{"external_product_id": "id", "title": "title", "price_cents": "price", "currency": "currency", "status": "status", "stock_total": "stock"} {
		spec.Fields = append(spec.Fields, ingestion.FieldSpec{Target: target, Source: "$." + source, Required: true})
	}
	spec.Collections = []ingestion.CollectionSpec{{Target: "skus", Source: "$.skus", KeyField: "id", Fields: []ingestion.FieldSpec{{Target: "external_sku_id", Source: "$.id", Required: true}, {Target: "price_cents", Source: "$.price"}, {Target: "stock", Source: "$.stock"}}}}
	raw, err := json.Marshal(spec)
	require.NoError(t, err)
	compiled, err := ingestion.Compile(raw)
	require.NoError(t, err)
	profile := map[string]any{"tenant_id": 1, "platform_id": f.shop.PlatformID, "resource_type": "product", "name": "sync-test"}
	require.NoError(t, q.Table("mapping_profile").Clauses(clause.Returning{}).Create(profile).Error)
	version := map[string]any{"tenant_id": 1, "profile_id": profile["id"], "version_no": 1, "status": "published", "spec": json.RawMessage(raw), "checksum": compiled.Checksum}
	require.NoError(t, q.Table("mapping_version").Clauses(clause.Returning{}).Create(version).Error)
	f.mappingID = version["id"].(int64)
	options, err := json.Marshal(map[string]any{"mapping_version_id": f.mappingID, "authorization_generation": 1, "max_pages_per_run": 2, "page_size": 100})
	require.NoError(t, err)
	job := map[string]any{"tenant_id": 1, "shop_id": f.shop.ID, "resource_type": "product", "cron": "0 0 1 1 *", "enabled": true, "options": json.RawMessage(options)}
	require.NoError(t, q.Table("sync_job").Clauses(clause.Returning{}).Create(job).Error)
	f.jobID = job["id"].(int64)
	f.rebuild(t)
	management, err := syncmodule.NewManagement(f.engineStore, f.engine)
	require.NoError(t, err)
	profiles, err := identity.NewProfileService(newProfileStore(f.database))
	require.NoError(t, err)
	require.NoError(t, syncmodule.RegisterRoutes(f.router, f.identityFixture.service, profiles, management))
	return f
}

func (f *syncFixture) rebuild(t *testing.T) {
	t.Helper()
	platforms := connector.NewRegistry()
	require.NoError(t, platforms.Register(f.platform))
	var err error
	f.engine, f.engineStore, err = buildSyncRuntime(f.database, f.redis.cache, f.credentialFixture.service, platforms)
	require.NoError(t, err)
}
func (f *syncFixture) run(t *testing.T) repo.Run {
	t.Helper()
	run, err := f.engine.StartRun(logger.WithTraceID(f.ctx, "sync-integration-trace"), f.jobID, "manual")
	require.NoError(t, err)
	require.Equal(t, f.actor.UserID, *run.TriggeredBy)
	return run
}
func (f *syncFixture) readRun(t *testing.T, id int64) repo.Run {
	t.Helper()
	run, err := f.engineStore.ReadRun(f.ctx, id)
	require.NoError(t, err)
	return run
}
func (f *syncFixture) cursor(t *testing.T) string {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var value string
	require.NoError(t, q.Table("sync_cursor").Select("cursor_value").Where("shop_id=?", f.shop.ID).Take(&value).Error)
	return value
}
func (f *syncFixture) execute(t *testing.T, run repo.Run) {
	t.Helper()
	require.NoError(t, f.engine.ExecuteDispatch(f.ctx, f.jobID, run.ID, 1, run.Stats.Dispatch))
}
