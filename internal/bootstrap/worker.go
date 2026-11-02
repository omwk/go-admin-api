package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/hibiken/asynq"
	"github.com/omwk/go-admin-api/internal/kernel/cache"
	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/kernel/logger"
	"github.com/omwk/go-admin-api/internal/kernel/outbox"
	"github.com/omwk/go-admin-api/internal/modules/identity/authz"
	"github.com/omwk/go-admin-api/internal/worker"
)

// RunWorker composes durable outbox consumption and the singleton scheduler.
// Domain sync orchestration is deliberately not installed by this infrastructure batch.
func RunWorker(ctx context.Context, cfg *config.Config, database *db.DB, shared *cache.Cache, log *slog.Logger) (err error) {
	ctx = logger.With(ctx, log)
	publisher := worker.NewQueuePublisher(cfg.Redis)
	defer func() { err = errors.Join(err, publisher.Close()) }()
	dispatcher, err := outbox.NewDispatcher(database, publisher, func(ctx context.Context, event outbox.Event, _ error) {
		log.ErrorContext(ctx, "outbox attempts exhausted", "module", "outbox", "action", "exhausted", "event_id", event.ID, "attempts", event.Attempts)
	})
	if err != nil {
		return err
	}
	consumer, err := outbox.NewConsumer(dispatcher, shared, authorizationEventHandler(shared))
	if err != nil {
		return err
	}
	server, err := worker.NewQueueServer(cfg.Redis, cfg.Worker, log, consumer.Handle, func(context.Context, worker.TaskPayload) error {
		return fmt.Errorf("sync orchestration is not installed: %w", asynq.SkipRetry)
	})
	if err != nil {
		return err
	}
	leader, err := worker.NewLeader(shared, "worker:schedule:leader", 10*time.Second)
	if err != nil {
		return err
	}
	source := func(ctx context.Context) ([]worker.Schedule, error) {
		jobs, err := NewScheduleStore(database).ListEnabledJobs(ctx, 0)
		if err != nil {
			return nil, err
		}
		rows := make([]worker.Schedule, 0, len(jobs))
		for _, job := range jobs {
			rows = append(rows, worker.Schedule{JobID: job.JobID, Cron: job.Cron})
		}
		return rows, nil
	}
	loopCtx, cancel := context.WithCancel(ctx)
	var loops sync.WaitGroup
	loops.Add(2)
	go func() {
		defer loops.Done()
		pollOutbox(loopCtx, dispatcher, log)
	}()
	go func() {
		defer loops.Done()
		if err := leader.RunScheduled(loopCtx, func(ctx context.Context) error { return publisher.Schedule(ctx, source, time.Now()) }); err != nil {
			log.ErrorContext(loopCtx, "scheduler stopped", "module", "worker", "action", "schedule")
		}
	}()
	err = server.Run(ctx)
	cancel()
	loops.Wait()
	return err
}

func pollOutbox(ctx context.Context, dispatcher *outbox.Dispatcher, log *slog.Logger) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := dispatcher.RunOnce(ctx); err != nil && ctx.Err() == nil {
				log.ErrorContext(ctx, "outbox dispatch failed", "module", "outbox", "action", "dispatch")
			}
		}
	}
}

func authorizationEventHandler(shared *cache.Cache) func(context.Context, outbox.Event) error {
	return func(ctx context.Context, event outbox.Event) error {
		if event.EventType != "authorization.invalidate" || event.TenantID <= 0 {
			return errors.New("unsupported outbox event")
		}
		var payload struct {
			UserIDs []int64 `json:"user_ids"`
		}
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return errors.New("invalid authorization event")
		}
		for _, user := range payload.UserIDs {
			if user <= 0 {
				return errors.New("invalid authorization user identifier")
			}
		}
		if authz.New(shared).Pending(ctx, event.TenantID, payload.UserIDs) {
			return errors.New("authorization invalidation pending")
		}
		return nil
	}
}
