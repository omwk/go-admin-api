package bootstrap

import (
	"context"
	"errors"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/modules/catalog"
	"github.com/omwk/go-admin-api/internal/testenv"
	"github.com/stretchr/testify/require"
)

func TestExportIntegrationKillWorkerDuringUploadAndRestart(t *testing.T) {
	f := newExportFixture(t, 2001)
	job := f.create(t, catalog.ExportRequest{})
	q, cancel := f.database.WithContext(f.ctx)
	var schema string
	require.NoError(t, q.Raw("SELECT current_schema()").Scan(&schema).Error)
	cancel()
	dsn := testenv.Lookup("TEST_DATABASE_URL")
	if strings.Contains(dsn, "://") {
		parsed, err := url.Parse(dsn)
		require.NoError(t, err)
		params := parsed.Query()
		params.Set("search_path", schema+",public")
		parsed.RawQuery = params.Encode()
		dsn = parsed.String()
	} else {
		dsn += " search_path=" + schema + ",public"
	}
	binary, err := os.Executable()
	require.NoError(t, err)
	marker := filepath.Join(t.TempDir(), "upload-started")
	child := exec.CommandContext(f.ctx, binary, "-test.run=^TestExportIntegrationCrashChild$")
	child.Env = append(os.Environ(), "EXPORT_CRASH_DSN="+dsn, "EXPORT_CRASH_ID="+strconv.FormatInt(job.ID, 10), "EXPORT_CRASH_MARKER="+marker)
	child.Stdout, child.Stderr = os.Stdout, os.Stderr
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
	require.Equal(t, "running", f.read(t, job.ID).Status)
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE product_spu SET title='after-crash'"))
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE export_job SET started_at=now()-interval '6 minutes' WHERE id=?", job.ID))
	// Reconstruct the runtime; no state survives outside the database snapshot.
	restarted, err := catalog.NewExportService(NewExportStore(f.database, "export-tests"), f.objects, newAdminStore(f.database).Authorize)
	require.NoError(t, err)
	require.NoError(t, restarted.Execute(f.ctx, 1, job.ID))
	result := f.read(t, job.ID)
	require.Equal(t, 2, result.Attempt)
	require.EqualValues(t, 2001, result.RowCount)
	records := f.records(t, result.ObjectKey)
	require.Len(t, records, 2002)
	for _, row := range records[1:] {
		require.NotEqual(t, "after-crash", row[1])
	}
}

type exportCrashObject struct {
	*exportMemory
	marker string
}

func (m *exportCrashObject) Put(ctx context.Context, _ string, reader io.Reader, _ int64) (int64, error) {
	// Block only after the pipe has streamed its first bytes, outside the snapshot transaction.
	var first [512]byte
	if _, err := io.ReadFull(reader, first[:]); err != nil {
		return 0, err
	}
	if err := os.WriteFile(m.marker, []byte("streaming"), 0600); err != nil {
		return 0, err
	}
	<-ctx.Done()
	return 0, ctx.Err()
}

func TestExportIntegrationCrashChild(t *testing.T) {
	dsn := os.Getenv("EXPORT_CRASH_DSN")
	if dsn == "" {
		t.Skip("subprocess entry only")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	database, err := db.Open(ctx, config.DB{DSN: dsn, MaxOpen: 5, MaxIdle: 2, ConnMaxLifetime: time.Minute, TxnTimeout: testenv.Timeouts(t).DB.TxnTimeout})
	require.NoError(t, err)
	defer func() { require.NoError(t, database.Close()) }()
	id, err := strconv.ParseInt(os.Getenv("EXPORT_CRASH_ID"), 10, 64)
	require.NoError(t, err)
	objects := &exportCrashObject{exportMemory: newExportMemory(), marker: os.Getenv("EXPORT_CRASH_MARKER")}
	service, err := catalog.NewExportService(NewExportStore(database, "export-tests"), objects, newAdminStore(database).Authorize)
	require.NoError(t, err)
	err = service.Execute(ctx, 1, id)
	require.True(t, errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled))
}
