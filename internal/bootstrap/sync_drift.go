package bootstrap

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/scope"
	"github.com/omwk/go-admin-api/internal/modules/ingestion"
	"github.com/omwk/go-admin-api/internal/modules/sync/repo"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type schemaBaseline struct {
	ID, TenantID, PlatformID int64
	ResourceType             string
	Version                  int
	Fingerprint              string
	Fields                   ingestion.Structure `gorm:"serializer:json"`
	SampleRawID              int64
	Status                   string
}

const schemaBaselineColumns = "id,tenant_id,platform_id,resource_type,version,fingerprint,fields,sample_raw_id,status"

// The platform row serializes all shops for baseline lifecycle and rolling dedup.
func lockSchema(q *gorm.DB, tenant, shop int64) (int64, error) {
	var platform int64
	err := q.Table("platform p").Select("p.id").Joins("JOIN shop s ON s.platform_id=p.id").Where("s.id=? AND s.tenant_id=? AND s.deleted_at IS NULL AND s.status='active'", shop, tenant).Clauses(clause.Locking{Strength: "UPDATE", Table: clause.Table{Name: "p"}}).Take(&platform).Error
	return platform, err
}

func (s *syncEngineStore) commitStructure(q *gorm.DB, a scope.WorkAccess, run *repo.Run, page repo.Page) error {
	if run.Stats.ReplayID > 0 || (len(page.Items) == 0 && len(run.Stats.Structure) == 0) {
		return nil
	}
	platform, err := lockSchema(q, a.TenantID(), a.ShopID())
	if err != nil {
		return err
	}
	if run.Stats.Structure == nil {
		run.Stats.Structure = ingestion.Structure{}
	}
	var active schemaBaseline
	err = q.Table("schema_baseline").Select(schemaBaselineColumns).Where("tenant_id=? AND platform_id=? AND resource_type=? AND status='active'", a.TenantID(), platform, a.Resource()).Take(&active).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	observed := ingestion.Structure{}
	for _, item := range page.Items {
		observed.Merge(item.Structure)
		run.Stats.Structure.Merge(item.Structure)
		if run.Stats.StructureSample == 0 {
			run.Stats.StructureSample = item.Raw.ID
		}
		if active.ID > 0 {
			for _, path := range item.MissingRequired {
				if old, exists := active.Fields[path]; exists {
					if err := persistDrift(q, a, platform, active.ID, item.Raw.ID, ingestion.Drift{Kind: "field_removed", Path: path, OldType: old, Severity: "critical"}); err != nil {
						return err
					}
				}
			}
			for _, change := range ingestion.CompareStructure(active.Fields, item.Structure, page.Required) {
				if change.Kind == "field_removed" && change.Severity == "critical" {
					if err := persistDrift(q, a, platform, active.ID, item.Raw.ID, change); err != nil {
						return err
					}
				}
			}
		}
	}
	if active.ID > 0 && len(page.Items) > 0 {
		for _, change := range ingestion.CompareStructure(active.Fields, observed, page.Required) {
			if err := persistDrift(q, a, platform, active.ID, driftSample(page, change), change); err != nil {
				return err
			}
		}
	}
	if page.HasMore {
		return nil
	}
	if run.Stats.CycleFailed {
		return nil
	}
	if active.ID > 0 {
		fingerprint, err := run.Stats.Structure.Fingerprint()
		if err != nil || fingerprint == active.Fingerprint {
			return err
		}
		return insertBaseline(q, a, platform, run.Stats.Structure, run.Stats.StructureSample, "superseded")
	}
	if run.Status != "success" {
		return nil
	}
	return s.warmBaseline(q, a, platform, *run)
}

func driftSample(page repo.Page, change ingestion.Drift) int64 {
	for _, item := range page.Items {
		types, found := item.Structure[change.Path]
		if (change.Kind == "field_removed" && !found) || (change.Kind != "field_removed" && found && types != change.OldType) {
			return item.Raw.ID
		}
	}
	return page.Items[0].Raw.ID
}

func (s *syncEngineStore) warmBaseline(q *gorm.DB, a scope.WorkAccess, platform int64, run repo.Run) error {
	var previous []repo.Run
	err := q.Table("sync_run r").Select("r.stats").Joins("JOIN shop s ON s.id=r.shop_id AND s.tenant_id=r.tenant_id").Where("r.tenant_id=? AND s.platform_id=? AND r.resource_type=? AND r.id<>? AND r.status='success' AND r.finished_at IS NOT NULL AND COALESCE((r.stats->>'cycle_failed')::boolean,false)=false AND r.stats->>'replay_id' IS NULL AND jsonb_exists(r.stats,'structure')", a.TenantID(), platform, a.Resource(), run.ID).Order("r.finished_at,r.id").Limit(2).Find(&previous).Error
	if err != nil {
		return err
	}
	if len(previous) < 2 {
		return nil
	}
	fields := ingestion.Structure{}
	for _, prior := range previous {
		fields.Merge(prior.Stats.Structure)
	}
	fields.Merge(run.Stats.Structure)
	return insertBaseline(q, a, platform, fields, run.Stats.StructureSample, "active")
}

func insertBaseline(q *gorm.DB, a scope.WorkAccess, platform int64, fields ingestion.Structure, sample int64, status string) error {
	var version int
	if err := q.Table("schema_baseline").Select("COALESCE(max(version),0)+1").Where("tenant_id=? AND platform_id=? AND resource_type=?", a.TenantID(), platform, a.Resource()).Scan(&version).Error; err != nil {
		return err
	}
	fingerprint, err := fields.Fingerprint()
	if err != nil {
		return err
	}
	row := schemaBaseline{TenantID: a.TenantID(), PlatformID: platform, ResourceType: a.Resource(), Version: version, Fingerprint: fingerprint, Fields: fields, SampleRawID: sample, Status: status}
	return q.Table("schema_baseline").Create(&row).Error
}

func persistDrift(q *gorm.DB, a scope.WorkAccess, platform, baseline, sample int64, change ingestion.Drift) error {
	var count int64
	// Ignored events form the explicit path whitelist, across later baselines.
	if err := q.Table("schema_drift_event").Where("tenant_id=? AND platform_id=? AND resource_type=? AND path=? AND status='ignored'", a.TenantID(), platform, a.Resource(), change.Path).Count(&count).Error; err != nil || count > 0 {
		return err
	}
	err := q.Table("schema_drift_event").Where("tenant_id=? AND baseline_id=? AND kind=? AND path=? AND detected_at>?", a.TenantID(), baseline, change.Kind, change.Path, time.Now().UTC().Add(-24*time.Hour)).Count(&count).Error
	if err != nil || count > 0 {
		return err
	}
	row := map[string]any{"tenant_id": a.TenantID(), "platform_id": platform, "shop_id": a.ShopID(), "resource_type": a.Resource(), "baseline_id": baseline, "kind": change.Kind, "path": change.Path, "old_type": change.OldType, "new_type": change.NewType, "severity": change.Severity, "sample_raw_id": sample}
	if err := q.Table("schema_drift_event").Clauses(clause.Returning{}).Create(row).Error; err != nil {
		return err
	}
	if change.Severity != "critical" {
		return nil
	}
	payload, err := json.Marshal(map[string]any{"drift_id": row["id"], "job_id": a.JobID(), "run_id": a.RunID()})
	if err != nil {
		return err
	}
	return q.Table("notification").Create(map[string]any{"tenant_id": a.TenantID(), "channel": "inbox", "target": fmt.Sprintf("shop:%d", a.ShopID()), "template": "schema_drift_critical", "payload": json.RawMessage(payload), "dedup_key": fmt.Sprintf("drift:%v", row["id"])}).Error
}
