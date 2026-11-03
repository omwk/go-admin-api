package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/hibiken/asynq"
	"github.com/oklog/ulid/v2"
	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/kernel/logger"
	"github.com/omwk/go-admin-api/internal/kernel/outbox"
)

const OutboxTask = "outbox:deliver"
const SyncTask = "sync:job"

type Delivery struct {
	EventID int64 `json:"event_id"`
	Attempt int   `json:"attempt"`
}

type QueuePublisher struct{ client *asynq.Client }

func RedisOptions(cfg config.Redis) asynq.RedisClientOpt {
	return asynq.RedisClientOpt{Addr: cfg.Addr, Password: cfg.Password, DB: cfg.DB,
		DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second}
}

func NewQueuePublisher(cfg config.Redis) *QueuePublisher {
	return &QueuePublisher{client: asynq.NewClient(RedisOptions(cfg))}
}

func (p *QueuePublisher) Close() error { return p.client.Close() }

func (p *QueuePublisher) Publish(ctx context.Context, event outbox.Event) error {
	if db.InTransaction(ctx) || event.ID <= 0 || event.Attempts <= 0 {
		return errors.New("queue publishing requires valid identifiers outside a transaction")
	}
	raw, err := json.Marshal(Delivery{EventID: event.ID, Attempt: event.Attempts})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	queue := "notify"
	if event.EventType == "sync.dispatch" {
		queue = "sync"
	}
	_, err = p.client.EnqueueContext(ctx, asynq.NewTask(OutboxTask, raw), asynq.Queue(queue), asynq.MaxRetry(0),
		asynq.TaskID(fmt.Sprintf("outbox-%d-%d", event.ID, event.Attempts)))
	if errors.Is(err, asynq.ErrTaskIDConflict) {
		return nil
	}
	return err
}

// QueueServer owns the real asynq lifecycle. Its handler base context survives
// SIGTERM for draining; asynq cancels it when the shutdown grace expires.
type QueueServer struct {
	server *asynq.Server
	mux    *asynq.ServeMux
}

func NewQueueServer(redis config.Redis, cfg config.Worker, log *slog.Logger, consume func(context.Context, int64, int) error, syncJob Handler) (*QueueServer, error) {
	return newQueueServer(redis, cfg, log, consume, syncJob, 25*time.Second)
}

func newQueueServer(redis config.Redis, cfg config.Worker, log *slog.Logger, consume func(context.Context, int64, int) error, syncJob Handler, grace time.Duration) (*QueueServer, error) {
	if cfg.Concurrency <= 0 || log == nil || consume == nil || syncJob == nil {
		return nil, errors.New("worker dependencies and concurrency are required")
	}
	queues := map[string]int{}
	for _, name := range cfg.Queues {
		switch name {
		case "sync", "refresh", "billing", "notify":
			queues[name] = 1
		default:
			return nil, errors.New("worker queue is unsupported")
		}
	}
	if len(queues) != 4 {
		return nil, errors.New("worker requires sync, refresh, billing and notify queues")
	}
	mux := asynq.NewServeMux()
	mux.Use(traceTasks(log))
	mux.HandleFunc(OutboxTask, func(ctx context.Context, task *asynq.Task) error {
		var delivery Delivery
		if err := decodeIdentifiers(task.Payload(), &delivery); err != nil || delivery.EventID <= 0 || delivery.Attempt <= 0 {
			return fmt.Errorf("invalid outbox identifiers: %w", asynq.SkipRetry)
		}
		return consume(ctx, delivery.EventID, delivery.Attempt)
	})
	mux.HandleFunc(SyncTask, func(ctx context.Context, task *asynq.Task) error {
		payload, err := DecodeTaskPayload(task.Payload())
		if err != nil {
			return fmt.Errorf("invalid sync identifiers: %w", asynq.SkipRetry)
		}
		return syncJob(ctx, payload)
	})
	srv := asynq.NewServer(RedisOptions(redis), asynq.Config{Concurrency: cfg.Concurrency, Queues: queues,
		ShutdownTimeout: grace, TaskCheckInterval: 100 * time.Millisecond, Logger: queueLogger{log}})
	return &QueueServer{server: srv, mux: mux}, nil
}

func (s *QueueServer) Run(ctx context.Context) error {
	if err := s.server.Start(s.mux); err != nil {
		return err
	}
	<-ctx.Done()
	s.server.Stop()
	done := make(chan struct{})
	go func() { s.server.Shutdown(); close(done) }()
	select {
	case <-done:
		return nil
	case <-time.After(29 * time.Second):
		return errors.New("worker shutdown exceeded deadline")
	}
}

func traceTasks(log *slog.Logger) asynq.MiddlewareFunc {
	return func(next asynq.Handler) asynq.Handler {
		return asynq.HandlerFunc(func(ctx context.Context, task *asynq.Task) error {
			ctx = logger.With(logger.WithTraceID(ctx, ulid.Make().String()), log)
			start := time.Now()
			err := next.ProcessTask(ctx, task)
			if err == nil && ctx.Err() != nil {
				err = ctx.Err()
			}
			log.InfoContext(ctx, "worker task processed", "module", "worker", "action", task.Type(),
				"trace_id", logger.TraceID(ctx), "duration_ms", time.Since(start).Milliseconds(), "success", err == nil)
			return err
		})
	}
}
