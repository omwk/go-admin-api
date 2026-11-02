package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/hibiken/asynq"
	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/kernel/logger"
	"github.com/robfig/cron/v3"
)

type Schedule struct {
	JobID int64
	Cron  string
}

type ScheduleSource func(context.Context) ([]Schedule, error)

// RunScheduled polls only while this instance owns the leader lease. Every tick
// renews ownership before doing IO; slot task IDs fence a late or restarted leader.
func (l *Leader) RunScheduled(ctx context.Context, tick func(context.Context) error) error {
	timer := time.NewTicker(500 * time.Millisecond)
	defer timer.Stop()
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		if err := l.Release(releaseCtx); err != nil {
			logger.From(ctx).Error("scheduler lease release failed", "module", "worker", "action", "leader_release")
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
			held, err := l.own(ctx)
			if err != nil || !held {
				continue // Fail closed on Redis outage or loss of leadership.
			}
			tickCtx, cancel := context.WithTimeout(ctx, l.ttl/2)
			err = tick(tickCtx)
			cancel()
			if err != nil && ctx.Err() == nil {
				logger.From(ctx).Error("scheduler tick failed", "module", "worker", "action", "schedule")
			}
		}
	}
}

func (l *Leader) own(ctx context.Context) (bool, error) {
	if l.lease == nil {
		return l.Acquire(ctx)
	}
	ok, err := l.lease.Renew(ctx, l.ttl)
	if err != nil || !ok {
		l.lease = nil
	}
	return ok, err
}

func (p *QueuePublisher) Schedule(ctx context.Context, source ScheduleSource, now time.Time) error {
	if source == nil || db.InTransaction(ctx) {
		return errors.New("scheduling requires a source outside a transaction")
	}
	jobs, err := source(ctx)
	if err != nil {
		return err
	}
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	for _, job := range jobs {
		schedule, err := parser.Parse(job.Cron)
		if err != nil {
			return errors.New("invalid persisted cron expression")
		}
		slot := schedule.Next(now.UTC().Truncate(time.Minute).Add(-time.Nanosecond))
		if slot.After(now) {
			continue
		}
		if err := p.enqueueSlot(ctx, job.JobID, slot); err != nil {
			return err
		}
	}
	return nil
}

func (p *QueuePublisher) enqueueSlot(ctx context.Context, jobID int64, slot time.Time) error {
	raw, err := json.Marshal(TaskPayload{JobID: jobID, Trigger: "cron"})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	_, err = p.client.EnqueueContext(ctx, asynq.NewTask(SyncTask, raw), asynq.Queue("sync"),
		asynq.TaskID(fmt.Sprintf("cron-%d-%d", jobID, slot.Unix())), asynq.Retention(24*time.Hour), asynq.MaxRetry(0))
	if errors.Is(err, asynq.ErrTaskIDConflict) {
		return nil
	}
	return err
}
