package bootstrap

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/shop/model"
	"github.com/omwk/go-admin-api/internal/modules/shop/refresh"
	"github.com/omwk/go-admin-api/internal/modules/shop/refresh/port"
	"github.com/stretchr/testify/require"
)

type credentialRefreshPlatform struct {
	calls  atomic.Int32
	handle func(context.Context, port.Request) (port.Result, error)
}

func (p *credentialRefreshPlatform) Refresh(ctx context.Context, r port.Request) (port.Result, error) {
	p.calls.Add(1)
	return p.handle(ctx, r)
}

type credentialRefreshFixture struct {
	*credentialFixture
	refreshStore *credentialRefreshStore
	executor     *refresh.Executor
	platform     *credentialRefreshPlatform
	target       refresh.Target
	now          time.Time
	output       []byte
}

func newCredentialRefreshFixture(t *testing.T) *credentialRefreshFixture {
	t.Helper()
	base := newCredentialFixture(t)
	created, _ := base.create(t, "oauth2")
	c, _ := base.readStored(t, created.ID, 1)
	output, err := json.Marshal(map[string]string{"private_value": base.randomValue(t)})
	require.NoError(t, err)
	f := &credentialRefreshFixture{credentialFixture: base, refreshStore: &credentialRefreshStore{database: base.database},
		target: refresh.Target{TenantID: 1, CredentialID: created.ID, InputVersionID: *c.CurrentVersionID},
		now:    time.Now().UTC().Truncate(time.Microsecond), output: output}
	f.platform = &credentialRefreshPlatform{handle: func(context.Context, port.Request) (port.Result, error) {
		return port.Result{Values: append([]byte(nil), f.output...), ExpiresAt: f.now.Add(96 * time.Hour), RefreshAt: f.now.Add(72 * time.Hour)}, nil
	}}
	f.executor = f.newExecutor(t, f.refreshStore)
	return f
}

func (f *credentialRefreshFixture) newExecutor(t *testing.T, store refresh.Store) *refresh.Executor {
	t.Helper()
	cfg := refresh.DefaultConfig()
	cfg.Jitter = func(time.Duration) (time.Duration, error) { return 0, nil }
	e, err := refresh.New(store, f.sealer, f.platform, cfg, func() time.Time { return f.now })
	require.NoError(t, err)
	return e
}

func (f *credentialRefreshFixture) prepare(t *testing.T) model.RefreshRun {
	t.Helper()
	run, err := f.executor.Prepare(f.ctx, f.target)
	require.NoError(t, err)
	return run
}

func (f *credentialRefreshFixture) storedRun(t *testing.T) model.RefreshRun {
	t.Helper()
	var run model.RefreshRun
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.Select(refreshRunColumns).Where("tenant_id=? AND credential_id=?", f.target.TenantID, f.target.CredentialID).
		Order("id DESC").Take(&run).Error)
	return run
}

func (f *credentialRefreshFixture) assertSafe(t *testing.T) {
	t.Helper()
	f.databaseSnapshot(t)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var rows []string
	require.NoError(t, q.Raw(`SELECT to_jsonb(r)::text FROM credential_refresh_run r UNION ALL
        SELECT to_jsonb(s)::text FROM credential_refresh_stage s UNION ALL
        SELECT to_jsonb(c)::text FROM credential_refresh_reconciliation c`).Scan(&rows).Error)
	encoded, err := json.Marshal(rows)
	require.NoError(t, err)
	f.noLeaks(t, "refresh database", string(encoded))
	f.noLeaks(t, "application logs", f.logs.text())
	f.noLeaks(t, "SQL logs", f.sqlLogs.text())
}

type credentialRefreshFaults struct {
	refresh.Store
	failStage, failPublish bool
	crashAt, phase         string
}

func (s *credentialRefreshFaults) Transaction(ctx context.Context, fn func(context.Context) error) error {
	s.phase = ""
	err := s.Store.Transaction(ctx, fn)
	if err == nil && s.phase != "" && s.phase == s.crashAt {
		s.crashAt = ""
		panic("simulated process exit")
	}
	return err
}
func (s *credentialRefreshFaults) InsertRun(ctx context.Context, r *model.RefreshRun) error {
	s.phase = r.Phase
	return s.Store.InsertRun(ctx, r)
}
func (s *credentialRefreshFaults) SaveRun(ctx context.Context, r model.RefreshRun) error {
	s.phase = r.Phase
	return s.Store.SaveRun(ctx, r)
}
func (s *credentialRefreshFaults) InsertStage(ctx context.Context, r *model.RefreshStage) error {
	if s.failStage {
		return refresh.ErrPersistence
	}
	return s.Store.InsertStage(ctx, r)
}
func (s *credentialRefreshFaults) PublishVersion(ctx context.Context, v *model.CredentialVersion, input int64, now time.Time) error {
	if s.failPublish {
		return refresh.ErrPersistence
	}
	return s.Store.PublishVersion(ctx, v, input, now)
}
