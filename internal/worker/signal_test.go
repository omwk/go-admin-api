package worker

import (
	"bufio"
	"context"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/hibiken/asynq"
	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/omwk/go-admin-api/internal/kernel/logger"
	"github.com/stretchr/testify/require"
)

func TestQueueSignalHelper(t *testing.T) {
	if os.Getenv("CH_WORKER_SIGNAL_HELPER") != "1" {
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer stop()
	log, err := logger.New(os.Stdout, "info", "json")
	require.NoError(t, err)
	srv, err := NewQueueServer(config.Redis{Addr: os.Getenv("CH_WORKER_SIGNAL_REDIS")},
		config.Worker{Concurrency: 1, Queues: []string{"sync", "refresh", "billing", "notify"}}, log,
		func(ctx context.Context, _ int64, _ int) error {
			log.InfoContext(ctx, "signal test task started")
			<-ctx.Done()
			return ctx.Err()
		}, func(context.Context, TaskPayload) error { return errors.New("unexpected sync task") })
	require.NoError(t, err)
	require.NoError(t, srv.Run(ctx))
}

func TestQueueProcessSIGTERMWithin30Seconds(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows cannot deliver POSIX SIGTERM; lifecycle cancellation/requeue is tested on all hosts")
	}
	r, _ := testQueueConfig(t)
	client := asynq.NewClient(RedisOptions(r))
	defer func() { require.NoError(t, client.Close()) }()
	_, err := client.Enqueue(asynq.NewTask(OutboxTask, []byte(`{"event_id":1,"attempt":1}`)), asynq.Queue("notify"))
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	executable, err := os.Executable()
	require.NoError(t, err)
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestQueueSignalHelper$")
	cmd.Env = append(os.Environ(), "CH_WORKER_SIGNAL_HELPER=1", "CH_WORKER_SIGNAL_REDIS="+r.Addr)
	output, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	started := make(chan struct{}, 1)
	scanned := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(output)
		for scanner.Scan() {
			if strings.Contains(scanner.Text(), "signal test task started") {
				started <- struct{}{}
			}
		}
		scanned <- scanner.Err()
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("child did not consume task")
	}
	start := time.Now()
	require.NoError(t, cmd.Process.Signal(syscall.SIGTERM))
	require.NoError(t, <-scanned)
	require.NoError(t, cmd.Wait())
	require.Less(t, time.Since(start), 30*time.Second)
	inspector := asynq.NewInspector(RedisOptions(r))
	defer func() { require.NoError(t, inspector.Close()) }()
	info, err := inspector.GetQueueInfo("notify")
	require.NoError(t, err)
	require.Equal(t, 1, info.Pending)
	require.Zero(t, info.Completed)
}
