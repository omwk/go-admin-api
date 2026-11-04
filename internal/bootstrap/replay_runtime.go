package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/omwk/go-admin-api/internal/kernel/cache"
	"github.com/omwk/go-admin-api/internal/kernel/outbox"
	syncmodule "github.com/omwk/go-admin-api/internal/modules/sync"
	"github.com/omwk/go-admin-api/internal/modules/sync/repo"
)

type replayDispatch struct {
	RunID     int64 `json:"run_id"`
	Processed int64 `json:"processed"`
}

func (s *syncEngineStore) emitReplay(ctx context.Context, r syncmodule.ReplayRun, processed int64) error {
	payload, err := json.Marshal(replayDispatch{RunID: r.ID, Processed: processed})
	if err != nil {
		return err
	}
	return outbox.Emit(ctx, s.database, outbox.Event{TenantID: r.TenantID, AggregateType: "replay_run", AggregateID: strconv.FormatInt(r.ID, 10), EventType: "replay.dispatch", Payload: payload})
}

func replayEventHandler(store *syncEngineStore, shared *cache.Cache, fallback func(context.Context, outbox.Event) error) func(context.Context, outbox.Event) error {
	return func(ctx context.Context, event outbox.Event) error {
		if event.EventType != "replay.dispatch" {
			return fallback(ctx, event)
		}
		var dispatch replayDispatch
		if json.Unmarshal(event.Payload, &dispatch) != nil || dispatch.RunID <= 0 || dispatch.Processed < 0 || event.TenantID <= 0 || event.AggregateType != "replay_run" || event.AggregateID != strconv.FormatInt(dispatch.RunID, 10) {
			return errors.New("invalid replay dispatch identity")
		}
		q, cancel := store.database.WithContext(ctx)
		defer cancel()
		var row struct {
			ShopID       int64
			ResourceType string
		}
		if err := q.Table("replay_run").Select("shop_id,resource_type").Where("tenant_id=? AND id=?", event.TenantID, dispatch.RunID).Take(&row).Error; err != nil {
			return err
		}
		job := repo.Job{TenantID: event.TenantID, ShopID: row.ShopID, Resource: row.ResourceType}
		return syncGuard(shared)(ctx, job, func(ctx context.Context) error {
			_, err := store.ProcessReplay(ctx, event.TenantID, dispatch.RunID, dispatch.Processed)
			return err
		})
	}
}
