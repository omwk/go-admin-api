package bootstrap

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/catalog"
	"github.com/omwk/go-admin-api/internal/modules/catalog/port"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/stretchr/testify/require"
)

type exportMemory struct {
	mu          sync.Mutex
	data        map[string][]byte
	dates       map[string]time.Time
	beforePut   func(context.Context, string) error
	deleteError bool
	corrupt     bool
	signed      string
	ttl         time.Duration
}

func newExportMemory() *exportMemory {
	return &exportMemory{data: map[string][]byte{}, dates: map[string]time.Time{}}
}

func (m *exportMemory) Put(ctx context.Context, key string, reader io.Reader, _ int64) (int64, error) {
	body, err := io.ReadAll(reader)
	if err != nil {
		return 0, err
	}
	if m.beforePut != nil {
		if err := m.beforePut(ctx, key); err != nil {
			return 0, err
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[key], m.dates[key] = body, time.Now().UTC()
	return int64(len(body)), nil
}

func (m *exportMemory) Get(_ context.Context, key string) (port.Object, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	body, ok := m.data[key]
	if !ok {
		return port.Object{}, errors.New("test object absent")
	}
	body = append([]byte(nil), body...)
	if m.corrupt && len(body) > 0 {
		body[len(body)-1] = 'X'
	}
	return port.Object{Body: io.NopCloser(bytes.NewReader(body)), Size: int64(len(body))}, nil
}

func (m *exportMemory) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.deleteError {
		return errors.New("injected delete failure")
	}
	delete(m.data, key)
	delete(m.dates, key)
	return nil
}

func (m *exportMemory) PresignGet(_ context.Context, key string, ttl time.Duration) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.signed, m.ttl = key, ttl
	return "private-test-download", nil
}

func (m *exportMemory) Scan(_ context.Context, prefix string, visit func(port.ObjectInfo) error) error {
	m.mu.Lock()
	var rows []port.ObjectInfo
	for key, date := range m.dates {
		if strings.HasPrefix(key, prefix) {
			rows = append(rows, port.ObjectInfo{Key: key, ModifiedAt: date})
		}
	}
	m.mu.Unlock()
	for _, row := range rows {
		if err := visit(row); err != nil {
			return err
		}
	}
	return nil
}

type exportFixture struct {
	*adminFixture
	store   *exportStore
	objects *exportMemory
	exports *catalog.ExportService
}

func newExportFixture(t *testing.T, count int) *exportFixture {
	t.Helper()
	f := &exportFixture{adminFixture: newAdminFixture(t), objects: newExportMemory()}
	f.contract = readCatalogOpenAPI(t)
	f.store = &exportStore{database: f.database, bucket: "export-tests"}
	var err error
	f.exports, err = catalog.NewExportService(f.store, f.objects, newAdminStore(f.database).Authorize)
	require.NoError(t, err)
	profiles, err := identity.NewProfileService(newProfileStore(f.database))
	require.NoError(t, err)
	require.NoError(t, catalog.RegisterExportRoutes(f.router, f.identityFixture.service, profiles, f.exports))
	if count > 0 {
		require.NoError(t, f.database.Exec(f.ctx, `INSERT INTO product_spu(tenant_id,title,status,source,price_min_cents,price_max_cents,sku_count,ext)
SELECT 1, 'frozen-'||n, 'active','manual',n,n+100,1,'{"_ingestion":{"raw_id":9},"raw":"excluded"}'::jsonb FROM generate_series(1,?) n`, count))
	}
	return f
}

func (f *exportFixture) create(t *testing.T, request catalog.ExportRequest) catalog.ExportJob {
	t.Helper()
	job, err := f.exports.Create(f.ctx, 1, f.actor.User.ID, request, nil)
	require.NoError(t, err)
	return job
}

func (f *exportFixture) read(t *testing.T, id int64) catalog.ExportJob {
	t.Helper()
	job, err := f.store.ReadExport(f.ctx, 1, f.actor.User.ID, id)
	require.NoError(t, err)
	return job
}

func (f *exportFixture) begin(t *testing.T, id int64) catalog.ExportJob {
	t.Helper()
	job, err := f.store.BeginExport(f.ctx, 1, id)
	require.NoError(t, err)
	return job
}

func (f *exportFixture) records(t *testing.T, key string) [][]string {
	t.Helper()
	object, err := f.objects.Get(f.ctx, key)
	require.NoError(t, err)
	defer func() { require.NoError(t, object.Body.Close()) }()
	records, err := csv.NewReader(object.Body).ReadAll()
	require.NoError(t, err)
	return records
}
