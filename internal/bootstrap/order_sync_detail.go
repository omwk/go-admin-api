package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"github.com/omwk/go-admin-api/internal/modules/connector"
	"github.com/omwk/go-admin-api/internal/modules/ingestion"
	"github.com/omwk/go-admin-api/internal/modules/order"
	syncmodule "github.com/omwk/go-admin-api/internal/modules/sync"
	"github.com/omwk/go-admin-api/internal/modules/sync/repo"
	"gorm.io/gorm"
	"strconv"
	"time"
)

func (o *orderAcquisition) details(ctx context.Context, work repo.Work, adapter connector.OrderPlatform, start time.Time) (bool, error) {
	if _, err := o.store.capability(ctx, "sync.page"); err != nil {
		return false, err
	}
	q, cancel := o.store.database.WithContext(ctx)
	var pending []order.SyncPending
	err := q.Select("id,tenant_id,shop_id,platform_id,external_order_id,revision,listed_updated_at,status,attempts,next_retry_at").
		Where("tenant_id=? AND shop_id=? AND status <> 'done' AND (next_retry_at IS NULL OR next_retry_at<=?)", work.Job.TenantID, work.Job.ShopID, o.store.now()).
		Order("id").Limit(200).Find(&pending).Error
	cancel()
	if err != nil {
		return false, err
	}
	for _, row := range pending {
		if time.Since(start) > 35*time.Second {
			break
		}
		var item connector.FetchItem
		fetchErr := o.call(ctx, work, func(ctx context.Context, credential connector.Credential) error {
			var err error
			item, err = adapter.FetchOrder(ctx, credential, row.ExternalOrderID)
			return err
		})
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		if fetchErr != nil {
			var platform *connector.PlatformError
			if errors.As(fetchErr, &platform) && platform.Kind == connector.ErrAuthExpired {
				return true, o.store.DeferRun(ctx, work.Run, "credential_expired", 0, true)
			}
			if err := o.store.failOrderDetail(ctx, work, row, "detail_unavailable"); err != nil {
				return false, err
			}
			if errors.As(fetchErr, &platform) && platform.Kind == connector.ErrRateLimited {
				break
			}
			continue
		}
		if item.ExternalID != row.ExternalOrderID || len(item.Payload) == 0 {
			if err := o.store.failOrderDetail(ctx, work, row, "invalid_detail_identity"); err != nil {
				return false, err
			}
			continue
		}
		if err := o.persistDetail(ctx, work, row, item); err != nil {
			return false, err
		}
	}
	return false, nil
}

func (o *orderAcquisition) persistDetail(ctx context.Context, work repo.Work, row order.SyncPending, item connector.FetchItem) error {
	refs, err := o.raw.SaveRaw(ctx, work.Job.ShopID, "order", []ingestion.RawItem{{ExternalID: item.ExternalID, Payload: item.Payload, FetchedAt: time.Now().UTC(), APIName: "order_detail"}})
	if err != nil {
		return err
	}
	if len(refs) != 1 {
		return errors.New("incomplete order raw disposition")
	}
	disposition := syncmodule.InterpretFetched(work, refs[0], item.Payload, o.handler)
	deleted, _ := disposition.Mapped.Values["deleted"].(bool)
	if disposition.Reason == "" && row.Revision != nil && !deleted {
		revision, err := strconv.ParseInt(fmt.Sprint(disposition.Mapped.Values["revision"]), 10, 64)
		if err != nil || revision < *row.Revision {
			disposition.Reason = "invalid_value"
		}
	}
	return o.store.orderTransaction(ctx, work, func(q *gorm.DB, run *repo.Run) error {
		before := run.Stats.Quarantined + run.Stats.Failed
		if err := o.store.commitItem(q, work.Access, run, disposition); err != nil {
			return err
		}
		if run.Stats.Quarantined+run.Stats.Failed > before {
			return deferOrderDetail(q, work, row, "detail_requires_review", o.store.now())
		}
		result := q.Model(&order.SyncPending{}).Where("tenant_id=? AND shop_id=? AND id=? AND status <> 'done'", work.Job.TenantID, work.Job.ShopID, row.ID).
			Updates(map[string]any{"status": "done", "reason_code": "", "next_retry_at": nil, "updated_at": time.Now().UTC()})
		if result.Error != nil {
			return result.Error
		}
		run.Stats.OrderCompleted += int(result.RowsAffected)
		run.Stats.Fetched += int(result.RowsAffected)
		return nil
	})
}

func (s *syncEngineStore) failOrderDetail(ctx context.Context, work repo.Work, row order.SyncPending, reason string) error {
	return s.orderTransaction(ctx, work, func(q *gorm.DB, _ *repo.Run) error { return deferOrderDetail(q, work, row, reason, s.now()) })
}

func deferOrderDetail(q *gorm.DB, work repo.Work, row order.SyncPending, reason string, now time.Time) error {
	delay := time.Second * time.Duration(1<<min(row.Attempts, 9))
	return syncChanged(q.Model(&order.SyncPending{}).Where("tenant_id=? AND shop_id=? AND id=? AND status <> 'done'", work.Job.TenantID, work.Job.ShopID, row.ID).
		Updates(map[string]any{"status": "pending", "attempts": gorm.Expr("attempts+1"), "next_retry_at": now.Add(delay), "reason_code": reason, "updated_at": time.Now().UTC()}))
}
