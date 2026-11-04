package bootstrap

import (
	"encoding/json"
	"errors"
	"time"

	syncmodule "github.com/omwk/go-admin-api/internal/modules/sync"
	"gorm.io/gorm"
)

func replaySelection(q *gorm.DB, r syncmodule.ReplayRun, s syncmodule.ReplayScope) *gorm.DB {
	q = q.Table("raw_dedup d").Where("d.tenant_id=? AND d.platform_id=? AND d.shop_id=? AND d.resource_type=?", r.TenantID, r.PlatformID, r.ShopID, r.ResourceType)
	switch s.Type {
	case "time":
		return q.Where("d.fetched_at>=? AND d.fetched_at<?", s.From, s.To)
	case "ids":
		if len(s.IDs) > 0 {
			return q.Where("d.raw_id IN ?", s.IDs)
		}
		return q.Where("d.raw_id BETWEEN ? AND ?", s.FromID, s.ToID)
	case "quarantine":
		return q.Where("EXISTS (SELECT 1 FROM quarantine_record z WHERE z.tenant_id=d.tenant_id AND z.shop_id=d.shop_id AND z.resource_type=d.resource_type AND z.raw_id=d.raw_id AND z.status='pending')")
	default:
		return q.Where("false")
	}
}

func freezeReplayMembers(q *gorm.DB, r *syncmodule.ReplayRun, s syncmodule.ReplayScope) error {
	// Fresh bulk loads can estimate both inputs as one row and choose a quadratic
	// nested loop. Keep the bounded set join linear even before auto-analyze.
	if err := q.Exec("SET LOCAL enable_nestloop=off").Error; err != nil {
		return err
	}
	selected := replaySelection(q.Session(&gorm.Session{}), *r, s).Select("DISTINCT d.raw_id,d.fetched_at").Order("d.raw_id,d.fetched_at").Limit(500001)
	var frozen struct{ Total, Materialized, MinID, MaxID int64 }
	err := q.Raw(`WITH selected AS MATERIALIZED (?), inserted AS (
 INSERT INTO replay_run_item(run_id,fetched_at,id,external_id,payload_sha256)
 SELECT ?::bigint,p.fetched_at,p.id,p.external_id,p.payload_sha256
 FROM selected d JOIN raw_payload p ON p.fetched_at=d.fetched_at AND p.id=d.raw_id
 WHERE p.tenant_id=? AND p.platform_id=? AND p.shop_id=? AND p.resource_type=?
 AND (SELECT count(*) FROM selected)<=500000
 ORDER BY p.fetched_at,p.id
 RETURNING id)
 SELECT (SELECT count(*) FROM selected) AS total,count(*) AS materialized,min(id) AS min_id,max(id) AS max_id FROM inserted`,
		selected, r.ID, r.TenantID, r.PlatformID, r.ShopID, r.ResourceType).Scan(&frozen).Error
	if err != nil {
		return err
	}
	if frozen.Total > 500000 || frozen.Total != frozen.Materialized {
		return replayInvalid()
	}
	r.Total = frozen.Total
	if r.Total == 0 {
		return nil
	}
	r.FromRawID, r.ToRawID = frozen.MinID, frozen.MaxID
	var upper syncmodule.ReplayKey
	if err := q.Table("replay_run_item").Select("fetched_at,id").Where("run_id=?", r.ID).Order("fetched_at DESC,id DESC").Take(&upper).Error; err != nil {
		return err
	}
	upper.FetchedAt = upper.FetchedAt.UTC()
	r.UpperKey, err = json.Marshal(upper)
	return err
}

func saveReplay(q *gorm.DB, r syncmodule.ReplayRun) error {
	if r.Processed != r.Stats.Processed() || r.Processed > r.Total {
		return errors.New("replay disposition counts are inconsistent")
	}
	stats, err := json.Marshal(r.Stats)
	if err != nil {
		return err
	}
	return syncChanged(q.Table("replay_run").Where("tenant_id=? AND id=?", r.TenantID, r.ID).Updates(map[string]any{
		"total": r.Total, "processed": r.Processed, "from_raw_id": r.FromRawID, "to_raw_id": r.ToRawID, "checkpoint_key": r.CheckpointKey, "upper_key": r.UpperKey,
		"stats": json.RawMessage(stats), "status": r.Status, "finished_at": r.FinishedAt}))
}

type replayMember struct {
	ID            int64
	FetchedAt     time.Time
	ExternalID    string
	PayloadSHA256 string
	Payload       json.RawMessage
	Outcome       string
}
