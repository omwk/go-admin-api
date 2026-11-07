package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/cache"
	"github.com/omwk/go-admin-api/internal/kernel/outbox"
	syncmodule "github.com/omwk/go-admin-api/internal/modules/sync"
)

type replayDispatch struct {
	RunID     int64 `json:"run_id"`
	Processed int64 `json:"processed"`
}

func (s *syncEngineStore) emitReplay(ctx context.Context, r syncmodule.ReplayRun, processed int64) error {
	return s.emitReplayAfter(ctx, r, processed, 0)
}

func (s *syncEngineStore) emitReplayAfter(ctx context.Context, r syncmodule.ReplayRun, processed int64, delay time.Duration) error {
	payload, err := json.Marshal(replayDispatch{RunID: r.ID, Processed: processed})
	if err != nil {
		return err
	}
	return s.database.Transaction(ctx, func(ctx context.Context) error {
		q, cancel := s.database.WithContext(ctx)
		defer cancel()
		ready := s.now().UTC().Add(delay)
		if delay == 0 {
			if err := q.Table("replay_run").Where("tenant_id=? AND id=?", r.TenantID, r.ID).Update("ready_at", ready).Error; err != nil {
				return err
			}
		}
		return outbox.Emit(ctx, s.database, outbox.Event{TenantID: r.TenantID, AggregateType: "replay_run", AggregateID: strconv.FormatInt(r.ID, 10), EventType: "replay.dispatch", Payload: payload, AvailableAt: ready})
	})
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
		_, err := store.processReplayWithGuard(ctx, event.TenantID, dispatch.RunID, dispatch.Processed, syncGuard(shared, store.settings))
		return err
	}
}
