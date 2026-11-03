package bootstrap

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/catalog"
	"github.com/omwk/go-admin-api/internal/modules/connector/platforms/mock"
	"github.com/omwk/go-admin-api/internal/modules/ingestion"
	"github.com/stretchr/testify/require"
)

func TestCatalogCategorySyncIntegrationTreeRerunMappingAndStaleCycle(t *testing.T) {
	f := newCategorySyncFixture(t, categoryTree(t))
	insertCategory(t, f, 0, "Category 01", "/Category 01", 1)
	shoesID := insertCategory(t, f, 0, "Shoes", "/Shoes", 1)
	insertCategory(t, f, shoesID, "Category 06", "/Shoes/Category 06", 2)

	first := f.run(t)
	f.execute(t, first)
	require.Equal(t, "success", f.readRun(t, first.ID).Status)
	require.EqualValues(t, 20, categoryTableCount(t, f))

	q, cancel := f.database.WithContext(f.ctx)
	var exactMapping struct {
		ID         int64
		CategoryID *int64
		Confidence float64
		Source     string
	}
	require.NoError(t, q.Table("category_mapping m").Select("m.id, m.category_id, m.confidence, m.source").
		Joins("JOIN platform_category p ON p.id=m.platform_category_id").Where("p.external_id='category-01'").Take(&exactMapping).Error)
	require.NotNil(t, exactMapping.CategoryID)
	require.Equal(t, 0.9, exactMapping.Confidence)
	require.Equal(t, "rule", exactMapping.Source)
	var manualMappingID int64
	require.NoError(t, q.Table("category_mapping m").Select("m.id").
		Joins("JOIN platform_category p ON p.id=m.platform_category_id").Where("p.external_id='category-02'").Take(&manualMappingID).Error)
	cancel()
	store := NewCategoryManagementStore(f.database)
	require.NoError(t, store.SetManualCategoryMapping(f.ctx, 1, manualMappingID, shoesID))

	before := categorySyncedAt(t, f, "category-01")
	second := f.run(t)
	f.execute(t, second)
	require.Equal(t, "success", f.readRun(t, second.ID).Status)
	require.EqualValues(t, 20, categoryTableCount(t, f))
	require.True(t, categorySyncedAt(t, f, "category-01").After(before))
	manual := categoryMapping(t, f, "category-02")
	require.NotNil(t, manual.CategoryID)
	require.Equal(t, shoesID, *manual.CategoryID)
	require.Equal(t, "manual", manual.Source)

	pool, _, err := store.ListCategoryMappingPool(f.ctx, 1, f.shop.PlatformID, 1, 100)
	require.NoError(t, err)
	var childCandidate *catalog.MappingPoolItem
	for i := range pool {
		if pool[i].Platform.ExternalID == "category-06" {
			childCandidate = &pool[i]
		}
	}
	require.NotNil(t, childCandidate)
	require.Nil(t, childCandidate.Mapping.CategoryID)
	require.Len(t, childCandidate.Candidates, 1)
	require.Equal(t, 0.6, childCandidate.Candidates[0].Confidence)

	removedTree := []json.RawMessage{categoryNode(t, "category-01", "", "Category 01")}
	f.platform = mock.NewCategoryDataset(removedTree)
	f.rebuild(t)
	third := f.run(t)
	f.execute(t, third)
	require.Equal(t, "success", f.readRun(t, third.ID).Status)
	require.EqualValues(t, 20, categoryTableCount(t, f))
	require.EqualValues(t, 19, staleCategoryCount(t, f, third.StartedAt))
	require.Equal(t, "manual", categoryMapping(t, f, "category-02").Source)

	staleBeforeFailure := categorySyncedAt(t, f, "category-02")
	f.platform.Fault = "500"
	f.rebuild(t)
	failedCycle := f.run(t)
	f.execute(t, failedCycle)
	failed := f.readRun(t, failedCycle.ID)
	require.Equal(t, "partial", failed.Status)
	require.True(t, failed.Stats.CycleFailed)
	require.Equal(t, staleBeforeFailure, categorySyncedAt(t, f, "category-02"))
}

func newCategorySyncFixture(t *testing.T, categories []json.RawMessage) *syncFixture {
	t.Helper()
	f := newSyncFixture(t, nil)
	f.platform = mock.NewCategoryDataset(categories)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	spec := ingestion.Spec{SchemaVersion: 1, Resource: string(ingestion.ResourceCategory), KeyField: "id", StatusTables: map[string]map[string]string{}, Fields: []ingestion.FieldSpec{
		{Target: "name", Source: "$.name", Required: true},
		{Target: "ext.parent_external_id", Source: "$.parent_external_id"},
	}}
	raw, err := json.Marshal(spec)
	require.NoError(t, err)
	compiled, err := ingestion.Compile(raw)
	require.NoError(t, err)
	var profileID int64
	require.NoError(t, q.Table("mapping_version").Select("profile_id").Where("id=?", f.mappingID).Take(&profileID).Error)
	require.NoError(t, q.Table("mapping_profile").Where("id=?", profileID).Update("resource_type", string(ingestion.ResourceCategory)).Error)
	require.NoError(t, q.Table("mapping_version").Where("id=?", f.mappingID).Updates(map[string]any{"spec": json.RawMessage(raw), "checksum": compiled.Checksum}).Error)
	options, err := json.Marshal(map[string]any{"mapping_version_id": f.mappingID, "authorization_generation": 1, "max_pages_per_run": 20, "page_size": 7})
	require.NoError(t, err)
	require.NoError(t, q.Table("sync_job").Where("id=?", f.jobID).Updates(map[string]any{"resource_type": string(ingestion.ResourceCategory), "cron": "0 3 * * *", "enabled": true, "options": json.RawMessage(options)}).Error)
	require.NoError(t, q.Table("platform").Where("id=?", f.shop.PlatformID).Update("capabilities", json.RawMessage(`{"resources":["product","category"],"incremental_modes":["page"]}`)).Error)
	f.rebuild(t)
	return f
}

func categoryTree(t *testing.T) []json.RawMessage {
	t.Helper()
	items := make([]json.RawMessage, 0, 20)
	for i := 1; i <= 2; i++ {
		items = append(items, categoryNode(t, fmt.Sprintf("category-%02d", i), "", fmt.Sprintf("Category %02d", i)))
	}
	for i := 3; i <= 8; i++ {
		parent := fmt.Sprintf("category-%02d", (i-3)/3+1)
		items = append(items, categoryNode(t, fmt.Sprintf("category-%02d", i), parent, fmt.Sprintf("Category %02d", i)))
	}
	for i := 9; i <= 20; i++ {
		parent := fmt.Sprintf("category-%02d", (i-9)/2+3)
		items = append(items, categoryNode(t, fmt.Sprintf("category-%02d", i), parent, fmt.Sprintf("Category %02d", i)))
	}
	return items
}

func categoryNode(t *testing.T, id, parent, name string) json.RawMessage {
	t.Helper()
	row := map[string]string{"id": id, "name": name}
	if parent != "" {
		row["parent_external_id"] = parent
	}
	raw, err := json.Marshal(row)
	require.NoError(t, err)
	return raw
}

func insertCategory(t *testing.T, f *syncFixture, parentID int64, name, path string, level int) int64 {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var id int64
	err := q.Raw("INSERT INTO category (tenant_id,parent_id,name,path,level,status) VALUES (1,?,?,?,?, 'active') RETURNING id", parentID, name, path, level).Scan(&id).Error
	require.NoError(t, err)
	return id
}

func categorySyncedAt(t *testing.T, f *syncFixture, externalID string) time.Time {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var value time.Time
	require.NoError(t, q.Table("platform_category").Select("synced_at").Where("tenant_id=1 AND platform_id=? AND external_id=?", f.shop.PlatformID, externalID).Take(&value).Error)
	return value
}

func categoryMapping(t *testing.T, f *syncFixture, externalID string) catalog.CategoryMapping {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var result catalog.CategoryMapping
	require.NoError(t, q.Table("category_mapping m").Select("m.id,m.tenant_id,m.platform_id,m.platform_category_id,m.category_id,m.confidence,m.source,m.created_at,m.updated_at").
		Joins("JOIN platform_category p ON p.id=m.platform_category_id").Where("p.tenant_id=1 AND p.platform_id=? AND p.external_id=?", f.shop.PlatformID, externalID).Take(&result).Error)
	return result
}

func staleCategoryCount(t *testing.T, f *syncFixture, cycleStarted time.Time) int64 {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var count int64
	require.NoError(t, q.Table("platform_category").Where("tenant_id=1 AND platform_id=? AND synced_at < ?", f.shop.PlatformID, cycleStarted).Count(&count).Error)
	return count
}

func categoryTableCount(t *testing.T, f *syncFixture) int64 {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var count int64
	require.NoError(t, q.Table("platform_category").Where("tenant_id=1 AND platform_id=?", f.shop.PlatformID).Count(&count).Error)
	return count
}
