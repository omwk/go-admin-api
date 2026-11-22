package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/hibiken/asynq"
	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/omwk/go-admin-api/internal/kernel/db"
)

// ThumbnailTask executes exclusively in the two reserved thumbnail slots.
const ThumbnailTask = "thumb:generate"

// ThumbnailPayload contains only the tenant-scoped file identity.
type ThumbnailPayload struct {
	TenantID int64 `json:"tenant_id"`
	FileID   int64 `json:"file_id"`
}

// EnqueueThumbnail publishes after commit; the hourly sweep owns abnormal retries.
func (p *QueuePublisher) EnqueueThumbnail(ctx context.Context, payload ThumbnailPayload) error {
	if db.InTransaction(ctx) || payload.TenantID <= 0 || payload.FileID <= 0 {
		return errors.New("thumbnail publishing requires committed file identifiers")
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	_, err = p.client.EnqueueContext(ctx, asynq.NewTask(ThumbnailTask, raw), asynq.Queue("thumb"),
		asynq.Timeout(60*time.Second), asynq.MaxRetry(0), asynq.Unique(10*time.Minute))
	if errors.Is(err, asynq.ErrDuplicateTask) {
		return nil
	}
	return err
}

// NewThumbnailQueueServer reserves two workers independently of other task traffic.
func NewThumbnailQueueServer(redis config.Redis, log *slog.Logger, generate func(context.Context, int64, int64) error) (*QueueServer, error) {
	if log == nil || generate == nil {
		return nil, errors.New("thumbnail worker dependencies required")
	}
	mux := asynq.NewServeMux()
	mux.Use(traceTasks(log))
	mux.HandleFunc(ThumbnailTask, func(ctx context.Context, task *asynq.Task) error {
		var payload ThumbnailPayload
		if err := decodeIdentifiers(task.Payload(), &payload); err != nil || payload.TenantID <= 0 || payload.FileID <= 0 {
			return fmt.Errorf("invalid thumbnail identifiers: %w", asynq.SkipRetry)
		}
		ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		return generate(ctx, payload.TenantID, payload.FileID)
	})
	server := asynq.NewServer(RedisOptions(redis), asynq.Config{Concurrency: 2, Queues: map[string]int{"thumb": 1},
		ShutdownTimeout: 25 * time.Second, TaskCheckInterval: 100 * time.Millisecond, Logger: queueLogger{log}})
	return &QueueServer{server: server, mux: mux}, nil
}
