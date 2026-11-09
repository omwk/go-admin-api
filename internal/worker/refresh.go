package worker

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/hibiken/asynq"
	"github.com/omwk/go-admin-api/internal/kernel/config"
)

// CredentialRefreshTask is routed exclusively to reserved maintenance workers.
const RefreshTask = "credential:refresh"

// NewRefreshQueueServer reserves execution slots independently of notification load.
func NewRefreshQueueServer(redis config.Redis, cfg config.Refresh, log *slog.Logger, consume func(context.Context, int64, int) error) (*QueueServer, error) {
	if log == nil || consume == nil {
		return nil, fmt.Errorf("refresh worker dependencies are required")
	}
	mux := asynq.NewServeMux()
	mux.Use(traceTasks(log))
	mux.HandleFunc(RefreshTask, func(ctx context.Context, task *asynq.Task) error {
		var delivery Delivery
		if err := decodeIdentifiers(task.Payload(), &delivery); err != nil || delivery.EventID <= 0 || delivery.Attempt <= 0 {
			return fmt.Errorf("invalid refresh identifiers: %w", asynq.SkipRetry)
		}
		return consume(ctx, delivery.EventID, delivery.Attempt)
	})
	server := asynq.NewServer(RedisOptions(redis), asynq.Config{Concurrency: cfg.ReservedConcurrency(), Queues: map[string]int{"refresh": 1},
		ShutdownTimeout: 25 * time.Second, TaskCheckInterval: 100 * time.Millisecond, Logger: queueLogger{log}})
	return &QueueServer{server: server, mux: mux}, nil
}
