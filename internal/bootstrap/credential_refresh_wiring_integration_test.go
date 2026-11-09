package bootstrap

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/omwk/go-admin-api/internal/kernel/logger"
	"github.com/omwk/go-admin-api/internal/kernel/outbox"
	"github.com/omwk/go-admin-api/internal/kernel/scope"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/shop"
	"github.com/omwk/go-admin-api/internal/modules/shop/model"
	"github.com/omwk/go-admin-api/internal/modules/shop/refresh"
	"github.com/omwk/go-admin-api/internal/modules/shop/refresh/port"
	"github.com/omwk/go-admin-api/internal/worker"
	"github.com/stretchr/testify/require"
)

type verifiedFixtureRefresh struct{ port.Refresher }

func (verifiedFixtureRefresh) RefreshProtocol() port.Protocol {
	return port.Protocol{Verified: true, ExclusiveGrant: true}
}
func credentialAuditForWiring(actor int64) model.CredentialAudit {
	return model.CredentialAudit{ActorID: &actor}
}

func wiredCredentialFixture(t *testing.T) (*credentialRefreshFixture, *credentialRefreshRuntime) {
	t.Helper()
	f := newCredentialRefreshFixture(t)
	r := &credentialRefreshRuntime{database: f.database, sealer: f.sealer, settings: config.Refresh{}, now: func() time.Time { return f.now }, adapters: map[string]port.ProtocolRefresher{"shopee": verifiedFixtureRefresh{f.platform}}}
	return f, r
}

func approveWiredMaintenance(t *testing.T, f *credentialRefreshFixture) {
	t.Helper()
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE shop_credential SET refresh_strategy='auto',refresh_at=? WHERE id=?", f.now, f.target.CredentialID))
	require.NoError(t, f.database.Transaction(f.ctx, func(ctx context.Context) error {
		c, _ := f.readStored(t, f.target.CredentialID, 1)
		return f.store.recordCredentialMaintenance(ctx, c, credentialAuditForWiring(f.actor.UserID))
	}))
}

// REQ-SHP-008: the live execution path rejects every identity except its own capability.
func TestCredentialRefreshWiringIntegrationMissingCapability(t *testing.T) {
	f, r := wiredCredentialFixture(t)
	run, err := r.prepare(f.ctx, 1, f.target.CredentialID, "manual", &f.actor.UserID)
	require.NoError(t, err)
	core, err := r.executor(true)
	require.NoError(t, err)
	_, err = core.Execute(f.ctx, f.target, run.ID)
	require.Error(t, err)
	work, err := scope.IssueWorkAccess(scope.BootstrapIssuer{}, 1, 9, 8, f.shop.ID, "product", 7, 6, []string{"credential.refresh"})
	require.NoError(t, err)
	_, err = core.Execute(scope.WithWorkAccess(f.ctx, work), f.target, run.ID)
	require.Error(t, err)
	require.Zero(t, f.platform.calls.Load())
}

func TestCredentialRefreshWiringIntegrationTwoWorkersOneDispatch(t *testing.T) {
	f, r := wiredCredentialFixture(t)
	run, err := r.prepare(f.ctx, 1, f.target.CredentialID, "manual", &f.actor.UserID)
	require.NoError(t, err)
	ready := make(chan struct{})
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); <-ready; results <- r.handle(f.ctx, run.ID) }()
	}
	close(ready)
	wg.Wait()
	close(results)
	for err := range results {
		require.NoError(t, err)
	}
	require.EqualValues(t, 1, f.platform.calls.Load())
	require.Equal(t, refresh.PhasePublished, f.storedRun(t).Phase)
	f.assertSafe(t)
}

func TestCredentialRefreshWiringIntegrationAutomaticApprovalAndGate(t *testing.T) {
	f, r := wiredCredentialFixture(t)
	_, err := r.prepare(f.ctx, 1, f.target.CredentialID, "schedule", nil)
	require.Error(t, err)
	approveWiredMaintenance(t, f)
	require.NoError(t, r.scan(f.ctx))
	run := f.storedRun(t)
	require.Equal(t, "schedule", run.Trigger)
	require.NoError(t, r.handle(f.ctx, run.ID))
	require.EqualValues(t, 1, f.platform.calls.Load())
	require.Equal(t, "success", f.storedRun(t).Status)
}

// REQ-SHP-008: real queue delivery carries only durable identifiers and sends once.
func TestCredentialRefreshWiringIntegrationScanQueueWorkerPublish(t *testing.T) {
	f, r := wiredCredentialFixture(t)
	approveWiredMaintenance(t, f)
	cfg := config.Redis{Addr: f.server.Addr()}
	publisher := worker.NewQueuePublisher(cfg)
	defer func() { require.NoError(t, publisher.Close()) }()
	dispatcher, err := outbox.NewDispatcher(f.database, publisher, nil)
	require.NoError(t, err)
	consumer, err := outbox.NewConsumer(dispatcher, f.redis.cache, r.events(func(context.Context, outbox.Event) error { return nil }))
	require.NoError(t, err)
	log, err := logger.New(f.logs, "info", "json")
	require.NoError(t, err)
	server, err := worker.NewRefreshQueueServer(cfg, config.Refresh{}, log, consumer.Handle)
	require.NoError(t, err)
	ctx, stop := context.WithCancel(f.ctx)
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()
	t.Cleanup(func() {
		stop()
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(30 * time.Second):
			t.Error("refresh worker did not stop")
		}
	})
	require.NoError(t, r.scan(f.ctx))
	require.NoError(t, dispatcher.RunOnce(f.ctx))
	require.Eventually(t, func() bool { return f.storedRun(t).Phase == refresh.PhasePublished }, 5*time.Second, 20*time.Millisecond)
	require.EqualValues(t, 1, f.platform.calls.Load())
	require.Equal(t, "schedule", f.storedRun(t).Trigger)
	_, version := f.readStored(t, f.target.CredentialID, 2)
	require.Equal(t, 2, version.VersionNo)
	f.assertSafe(t)
}

func TestCredentialRefreshWiringIntegrationCookieOnlyWarns(t *testing.T) {
	f, r := wiredCredentialFixture(t)
	cookie, _ := f.create(t, "cookie")
	f.target.CredentialID = cookie.ID
	approveWiredMaintenance(t, f)
	for _, trigger := range []string{"manual", "schedule", "on_demand"} {
		t.Run(trigger, func(t *testing.T) {
			_, err := r.prepare(f.ctx, 1, cookie.ID, trigger, &f.actor.UserID)
			require.Error(t, err)
		})
	}
	require.NoError(t, r.scan(f.ctx))
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var count int64
	require.NoError(t, q.Table("credential_refresh_run").Where("tenant_id=? AND credential_id=?", 1, cookie.ID).Count(&count).Error)
	require.Zero(t, count)
	require.Zero(t, f.platform.calls.Load())
}

func TestCredentialRefreshWiringIntegrationMissingEncryptionFailsClosed(t *testing.T) {
	f, r := wiredCredentialFixture(t)
	r.sealer = nil
	_, err := r.prepare(f.ctx, 1, f.target.CredentialID, "manual", &f.actor.UserID)
	require.Error(t, err)
	require.Zero(t, f.platform.calls.Load())
}

func TestCredentialRefreshWiringIntegrationHTTPRejectsForgedResources(t *testing.T) {
	f, r := wiredCredentialFixture(t)
	profiles, err := identity.NewProfileService(newProfileStore(f.database))
	require.NoError(t, err)
	require.NoError(t, shop.RegisterCredentialRefreshRoutes(f.router, f.identityFixture.service, profiles, r))
	response, _ := f.request(t, http.MethodPost, credentialPath(f.target.CredentialID)+"/refresh", map[string]any{"job_id": 1, "mapping_version_id": 1, "resource": "product"})
	require.Equal(t, 400, response.Code)
	require.Zero(t, f.platform.calls.Load())
	response, _ = f.request(t, http.MethodPost, credentialPath(f.target.CredentialID)+"/refresh", map[string]any{})
	require.Equal(t, 202, response.Code)
}

func TestCredentialRefreshWiringIntegrationPausedAndRevokedMaintenance(t *testing.T) {
	for _, revoked := range []bool{false, true} {
		t.Run(map[bool]string{false: "paused", true: "auto_revoked"}[revoked], func(t *testing.T) {
			f, r := wiredCredentialFixture(t)
			approveWiredMaintenance(t, f)
			require.NoError(t, f.database.Exec(f.ctx, "UPDATE shop SET status='paused' WHERE id=?", f.shop.ID))
			run, err := r.prepare(f.ctx, 1, f.target.CredentialID, "schedule", nil)
			require.NoError(t, err)
			if revoked {
				require.NoError(t, f.database.Exec(f.ctx, "UPDATE credential_maintenance SET enabled=false,generation=generation+1 WHERE credential_id=?", f.target.CredentialID))
				require.Error(t, r.handle(f.ctx, run.ID))
				require.Zero(t, f.platform.calls.Load())
				return
			}
			require.NoError(t, r.handle(f.ctx, run.ID))
			require.EqualValues(t, 1, f.platform.calls.Load())
		})
	}
}

func TestCredentialRefreshWiringIntegrationManualOwnerRevokedBeforeSend(t *testing.T) {
	f, r := wiredCredentialFixture(t)
	run, err := r.prepare(f.ctx, 1, f.target.CredentialID, "manual", &f.actor.UserID)
	require.NoError(t, err)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var owner int64
	require.NoError(t, q.Raw("INSERT INTO sys_user(tenant_id,username,password_hash,dept_id,status) SELECT tenant_id,'refresh-new-owner',password_hash,dept_id,'active' FROM sys_user WHERE id=? RETURNING id", f.actor.UserID).Scan(&owner).Error)
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE shop SET owner_user_id=? WHERE id=?", owner, f.shop.ID))
	require.Error(t, r.handle(f.ctx, run.ID))
	require.Zero(t, f.platform.calls.Load())
}
