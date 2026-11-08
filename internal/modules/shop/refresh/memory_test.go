package refresh

import (
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/crypto"
	"github.com/omwk/go-admin-api/internal/modules/shop/model"
	"github.com/omwk/go-admin-api/internal/modules/shop/refresh/port"
	"github.com/stretchr/testify/require"
)

type memoryRefresh struct {
	mu                                  sync.Mutex
	snap                                Snapshot
	runs                                map[int64]model.RefreshRun
	stages                              map[int64]model.RefreshStage
	reconciliations                     map[int64]string
	versions                            map[int64]model.CredentialVersion
	audits                              int
	crashAt, loseCommit                 string
	failStage, failPublish, failSending bool
	failReads                           int
}

func cloneMap[K comparable, V any](source map[K]V) map[K]V {
	dest := make(map[K]V, len(source))
	for k, v := range source {
		dest[k] = v
	}
	return dest
}

func (m *memoryRefresh) Transaction(ctx context.Context, fn func(context.Context) error) error {
	m.mu.Lock()
	snap, runs, stages, records, versions, audits := m.snap, cloneMap(m.runs), cloneMap(m.stages), cloneMap(m.reconciliations), cloneMap(m.versions), m.audits
	err := fn(ctx)
	if err != nil {
		m.snap, m.runs, m.stages, m.reconciliations, m.versions, m.audits = snap, runs, stages, records, versions, audits
	}
	phase := ""
	for _, r := range m.runs {
		if r.Generation == m.snap.Credential.RefreshGeneration {
			phase = r.Phase
		}
	}
	crash, lose := err == nil && phase == m.crashAt && phase != "", err == nil && phase == m.loseCommit && phase != ""
	if crash {
		m.crashAt = ""
	}
	if lose {
		m.loseCommit = ""
	}
	m.mu.Unlock()
	if crash {
		panic("simulated process exit")
	}
	if lose {
		return ErrPersistence
	}
	return err
}

func (m *memoryRefresh) Lock(_ context.Context, t Target) (Snapshot, error) {
	if m.failReads > 0 {
		m.failReads--
		return Snapshot{}, ErrPersistence
	}
	if m.snap.Credential.TenantID != t.TenantID || m.snap.Credential.ID != t.CredentialID {
		return Snapshot{}, ErrIneligible
	}
	return m.snap, nil
}
func (m *memoryRefresh) FindRun(_ context.Context, t Target, id int64) (*model.RefreshRun, error) {
	var found *model.RefreshRun
	for _, r := range m.runs {
		if r.TenantID != t.TenantID || r.CredentialID != t.CredentialID {
			continue
		}
		if id > 0 && r.ID == id {
			row := r
			return &row, nil
		}
		if id == 0 && r.InputVersionID == t.InputVersionID && (found == nil || found.Attempt < r.Attempt) {
			row := r
			found = &row
		}
	}
	return found, nil
}
func (m *memoryRefresh) InsertRun(_ context.Context, r *model.RefreshRun) error {
	r.ID = int64(len(m.runs) + 1)
	m.runs[r.ID] = *r
	return nil
}
func (m *memoryRefresh) SaveRun(_ context.Context, r model.RefreshRun) error {
	if m.failSending && r.Phase == PhaseSending {
		return ErrPersistence
	}
	m.runs[r.ID] = r
	return nil
}
func (m *memoryRefresh) SaveCredential(_ context.Context, c model.Credential) error {
	m.snap.Credential = c
	return nil
}
func (m *memoryRefresh) InsertStage(_ context.Context, s *model.RefreshStage) error {
	if m.failStage {
		return ErrPersistence
	}
	s.ID = s.RunID
	m.stages[s.ID] = *s
	return nil
}
func (m *memoryRefresh) Stage(_ context.Context, r model.RefreshRun) (model.RefreshStage, error) {
	s, ok := m.stages[*r.StagedResultID]
	if !ok {
		return s, ErrPersistence
	}
	return s, nil
}
func (m *memoryRefresh) PurgeStage(_ context.Context, s model.RefreshStage, now time.Time) error {
	s.CipherText, s.Nonce, s.PurgedAt = nil, nil, &now
	m.stages[s.ID] = s
	return nil
}
func (m *memoryRefresh) PublishVersion(_ context.Context, v *model.CredentialVersion, input int64, now time.Time) error {
	if m.failPublish {
		return ErrPersistence
	}
	previous := m.versions[input]
	purge := now.Add(90 * 24 * time.Hour)
	previous.RetiredAt, previous.PurgeAfter = &now, &purge
	m.versions[input] = previous
	v.ID = int64(len(m.versions) + 1)
	m.versions[v.ID] = *v
	m.snap.Version = *v
	return nil
}
func (m *memoryRefresh) Audit(context.Context, model.RefreshRun, string, string, time.Time) error {
	m.audits++
	return nil
}
func (m *memoryRefresh) Reconcile(_ context.Context, r model.RefreshRun, _ time.Time) error {
	m.reconciliations[r.ID] = r.Reason
	return nil
}
func (m *memoryRefresh) Resolve(_ context.Context, r model.RefreshRun, _ time.Time) error {
	delete(m.reconciliations, r.ID)
	return nil
}

type refreshPlatform struct {
	calls  atomic.Int32
	handle func(context.Context, port.Request) (port.Result, error)
}

func (p *refreshPlatform) Refresh(ctx context.Context, r port.Request) (port.Result, error) {
	p.calls.Add(1)
	return p.handle(ctx, r)
}

type executorFixture struct {
	store    *memoryRefresh
	platform *refreshPlatform
	executor *Executor
	target   Target
	now      time.Time
	output   []byte
}

func randomMaterial(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	_, err := crand.Read(b)
	require.NoError(t, err)
	return b
}

func newExecutorFixture(t *testing.T) *executorFixture {
	t.Helper()
	f := &executorFixture{target: Target{TenantID: 1, CredentialID: 7, InputVersionID: 1}, now: time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)}
	sealer, err := crypto.NewKeyring(map[int][]byte{1: randomMaterial(t, 32)}, 1)
	require.NoError(t, err)
	c := model.Credential{ID: 7, TenantID: 1, ShopID: 3, Kind: "oauth2", Status: "active", CurrentVersionID: &f.target.InputVersionID, RetryBudget: 5}
	aad, err := (crypto.CredentialAAD{TenantID: 1, ShopID: 3, CredentialID: 7, Kind: c.Kind, VersionNo: 1}).Encode()
	require.NoError(t, err)
	input := []byte(fmt.Sprintf(`{"value":"%s"}`, hex.EncodeToString(randomMaterial(t, 32))))
	defer clear(input)
	cipher, nonce, key, err := sealer.Seal(input, aad)
	require.NoError(t, err)
	v := model.CredentialVersion{ID: 1, TenantID: 1, CredentialID: 7, VersionNo: 1, CipherText: cipher, Nonce: nonce, KeyVersion: key, AAD: aad}
	f.store = &memoryRefresh{snap: Snapshot{Shop: model.Shop{ID: 3, TenantID: 1, Status: "active"}, Credential: c, Version: v},
		runs: map[int64]model.RefreshRun{}, stages: map[int64]model.RefreshStage{}, reconciliations: map[int64]string{}, versions: map[int64]model.CredentialVersion{1: v}}
	f.output = []byte(fmt.Sprintf(`{"value":"%s"}`, hex.EncodeToString(randomMaterial(t, 32))))
	f.platform = &refreshPlatform{handle: func(context.Context, port.Request) (port.Result, error) {
		return port.Result{Values: append([]byte(nil), f.output...), ExpiresAt: f.now.Add(96 * time.Hour), RefreshAt: f.now.Add(72 * time.Hour)}, nil
	}}
	cfg := DefaultConfig()
	cfg.Jitter = func(time.Duration) (time.Duration, error) { return 0, nil }
	f.executor, err = New(f.store, sealer, f.platform, cfg, func() time.Time { return f.now })
	require.NoError(t, err)
	return f
}
