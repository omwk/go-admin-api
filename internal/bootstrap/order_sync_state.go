package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/omwk/go-admin-api/internal/modules/connector"
	"github.com/omwk/go-admin-api/internal/modules/sync/repo"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"time"
)

type orderWindow struct {
	From       time.Time `json:"from"`
	Until      time.Time `json:"until"`
	Token      string    `json:"token"`
	Listed     bool      `json:"listed"`
	Capability string    `json:"capability"`
}

func (s *syncEngineStore) orderTransaction(ctx context.Context, work repo.Work, fn func(*gorm.DB, *repo.Run) error) error {
	if _, err := s.capability(ctx, "sync.page"); err != nil {
		return err
	}
	return s.database.Transaction(ctx, func(ctx context.Context) error {
		q, cancel := s.database.WithContext(ctx)
		defer cancel()
		if _, err := lockSchedule(q, work.Job.ID); err != nil {
			return err
		}
		if _, err := s.capability(ctx, "sync.page"); err != nil {
			return err
		}
		var current repo.Run
		if err := q.Table("sync_run").Select(syncRunColumns).Where("tenant_id=? AND id=? AND job_id=?", work.Job.TenantID, work.Run.ID, work.Job.ID).
			Clauses(clause.Locking{Strength: "UPDATE"}).Take(&current).Error; err != nil {
			return err
		}
		if !current.Stats.Pending {
			return errors.New("order run is no longer pending")
		}
		if err := s.fenceGrant(ctx, q, "sync", current.ID, current.SchedulingGeneration); err != nil {
			return err
		}
		if err := fn(q, &current); err != nil {
			return err
		}
		if err := saveSyncRun(q, current); err != nil {
			return err
		}
		return s.fenceGrant(ctx, q, "sync", current.ID, current.SchedulingGeneration)
	})
}

func (s *syncEngineStore) beginOrderWindow(ctx context.Context, work repo.Work, caps connector.OrderCapabilities) (orderWindow, error) {
	var window orderWindow
	err := s.orderTransaction(ctx, work, func(q *gorm.DB, _ *repo.Run) error {
		row := map[string]any{"tenant_id": work.Job.TenantID, "shop_id": work.Job.ShopID, "resource_type": "order", "cursor_type": "timestamp", "cursor_value": ""}
		if err := q.Table("sync_cursor").Clauses(clause.OnConflict{DoNothing: true}).Create(row).Error; err != nil {
			return err
		}
		var cursor struct {
			CursorValue string
			CursorMeta  json.RawMessage
		}
		if err := q.Table("sync_cursor").Select("cursor_value,cursor_meta").Where("tenant_id=? AND shop_id=? AND resource_type='order'", work.Job.TenantID, work.Job.ShopID).
			Clauses(clause.Locking{Strength: "UPDATE"}).Take(&cursor).Error; err != nil {
			return err
		}
		if err := json.Unmarshal(cursor.CursorMeta, &window); err != nil {
			return err
		}
		if !window.Until.IsZero() {
			return nil
		}
		from := time.Unix(0, 0).UTC()
		if cursor.CursorValue != "" {
			watermark, err := time.Parse(time.RFC3339Nano, cursor.CursorValue)
			if err != nil {
				return errors.New("invalid order watermark")
			}
			from = watermark.Add(-time.Duration(work.Job.OrderLookbackSeconds) * time.Second)
		}
		capability := "A:no_comparable_revision"
		if caps.ComparableRevision {
			capability = "A+B"
		}
		window = orderWindow{From: from, Until: work.Run.StartedAt.UTC(), Capability: capability}
		return saveOrderWindow(q, work, window)
	})
	return window, err
}

func saveOrderWindow(q *gorm.DB, work repo.Work, window orderWindow) error {
	raw, err := json.Marshal(window)
	if err != nil {
		return err
	}
	return syncChanged(q.Table("sync_cursor").Where("tenant_id=? AND shop_id=? AND resource_type='order'", work.Job.TenantID, work.Job.ShopID).
		Updates(map[string]any{"cursor_meta": json.RawMessage(raw), "cursor_type": "timestamp", "updated_at": time.Now().UTC()}))
}

func (s *syncEngineStore) discoverOrders(ctx context.Context, work repo.Work, window orderWindow, result connector.OrderListResult) error {
	return s.orderTransaction(ctx, work, func(q *gorm.DB, run *repo.Run) error {
		for _, summary := range result.Orders {
			if summary.ExternalID == "" || summary.UpdatedAt.IsZero() || summary.UpdatedAt.Before(window.From) || !summary.UpdatedAt.Before(window.Until) {
				return errors.New("invalid order list evidence")
			}
			if window.Capability == "A+B" && (summary.Revision == nil || *summary.Revision < 0) {
				return errors.New("missing comparable order revision")
			}
			row := map[string]any{"tenant_id": work.Job.TenantID, "shop_id": work.Job.ShopID, "platform_id": work.Job.PlatformID, "external_order_id": summary.ExternalID,
				"listed_updated_at": summary.UpdatedAt, "revision": summary.Revision, "status": "pending", "attempts": 0, "next_retry_at": nil, "reason_code": "", "updated_at": time.Now().UTC()}
			result := q.Table("order_sync_pending").Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "tenant_id"}, {Name: "shop_id"}, {Name: "external_order_id"}},
				DoUpdates: clause.AssignmentColumns([]string{"listed_updated_at", "revision", "status", "attempts", "next_retry_at", "reason_code", "updated_at"}),
				Where:     clause.Where{Exprs: []clause.Expression{clause.Expr{SQL: "order_sync_pending.listed_updated_at < EXCLUDED.listed_updated_at OR order_sync_pending.listed_updated_at IS NULL OR (order_sync_pending.listed_updated_at = EXCLUDED.listed_updated_at AND order_sync_pending.updated_at < ?)", Vars: []any{work.Run.StartedAt}}}}}).Create(row)
			if result.Error != nil {
				return result.Error
			}
			run.Stats.OrderDiscovered += int(result.RowsAffected)
		}
		return saveOrderWindow(q, work, window)
	})
}

func (s *syncEngineStore) finishOrderSlice(ctx context.Context, work repo.Work, window orderWindow) error {
	return s.orderTransaction(ctx, work, func(q *gorm.DB, run *repo.Run) error {
		var pending int64
		if err := q.Table("order_sync_pending").Where("tenant_id=? AND shop_id=? AND status <> 'done'", work.Job.TenantID, work.Job.ShopID).Count(&pending).Error; err != nil {
			return err
		}
		run.Stats.OrderPending = int(pending)
		run.Stats.Sequence++
		if window.Listed && pending == 0 {
			now := time.Now().UTC()
			run.Status, run.FinishedAt, run.Stats.Pending = "success", &now, false
			return syncChanged(q.Table("sync_cursor").Where("tenant_id=? AND shop_id=? AND resource_type='order'", work.Job.TenantID, work.Job.ShopID).
				Updates(map[string]any{"cursor_value": window.Until.Format(time.RFC3339Nano), "cursor_meta": json.RawMessage(`{}`), "updated_at": now}))
		}
		run.Status = "partial"
		run.Stats.Dispatch++
		run.Stats.Pending = true
		return s.emitRun(q.Statement.Context, *run, time.Second)
	})
}
