package bootstrap

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/testenv"
	"github.com/stretchr/testify/require"
)

func crashAfterFirstReplayBatch(t *testing.T, f *syncFixture, id int64) {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var location struct{ Database, Schema string }
	require.NoError(t, q.Raw("SELECT current_database() AS database, current_schema() AS schema").Scan(&location).Error)
	dsn := testenv.WithDatabase(t, testenv.Lookup("TEST_DATABASE_URL"), location.Database, location.Schema)
	binary, err := os.Executable()
	require.NoError(t, err)
	marker := filepath.Join(t.TempDir(), "batch-committed")
	child := exec.CommandContext(f.ctx, binary, "-test.run=^TestReplayIntegrationCrashChild$")
	child.Env = append(os.Environ(), "REPLAY_CRASH_DSN="+dsn, "REPLAY_CRASH_RUN="+strconv.FormatInt(id, 10), "REPLAY_CRASH_MARKER="+marker)
	child.Stdout, child.Stderr = os.Stdout, os.Stderr
	stdin, err := child.StdinPipe()
	require.NoError(t, err)
	defer func() { err := stdin.Close(); require.True(t, err == nil || errors.Is(err, os.ErrClosed)) }()
	require.NoError(t, child.Start())
	t.Cleanup(func() {
		if child.ProcessState == nil {
			require.NoError(t, child.Process.Kill())
			require.Error(t, child.Wait())
		}
	})
	require.Eventually(t, func() bool { _, err := os.Stat(marker); return err == nil }, 15*time.Second, 10*time.Millisecond)
	require.NoError(t, child.Process.Kill())
	require.Error(t, child.Wait())
}

func TestReplayIntegrationCrashChild(t *testing.T) {
	dsn := os.Getenv("REPLAY_CRASH_DSN")
	if dsn == "" {
		t.Skip("subprocess entry only")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	database, err := db.Open(ctx, config.DB{DSN: dsn, MaxOpen: 5, MaxIdle: 2, ConnMaxLifetime: time.Minute, TxnTimeout: testenv.Timeouts(t).DB.TxnTimeout, MigrateTimeout: testenv.Timeouts(t).DB.MigrateTimeout})
	require.NoError(t, err)
	defer func() { require.NoError(t, database.Close()) }()
	id, err := strconv.ParseInt(os.Getenv("REPLAY_CRASH_RUN"), 10, 64)
	require.NoError(t, err)
	store := newSyncEngineStore(database, registeredResourceHandlers())
	run, err := store.ProcessReplay(ctx, 1, id, 0)
	require.NoError(t, err)
	require.EqualValues(t, 200, run.Processed)
	require.NoError(t, os.WriteFile(os.Getenv("REPLAY_CRASH_MARKER"), []byte("committed"), 0600))
	var input [1]byte
	_, err = os.Stdin.Read(input[:])
	require.True(t, err == nil || errors.Is(err, io.EOF))
}
