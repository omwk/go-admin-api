package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/omwk/go-admin-api/internal/kernel/crypto"
	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/outbox"
	"github.com/omwk/go-admin-api/internal/kernel/scope"
	"github.com/omwk/go-admin-api/internal/modules/connector/platforms/mock"
	"github.com/omwk/go-admin-api/internal/modules/shop/model"
	"github.com/omwk/go-admin-api/internal/modules/shop/refresh"
	"github.com/omwk/go-admin-api/internal/modules/shop/refresh/port"
	"gorm.io/gorm/clause"
)

type credentialRefreshRuntime struct {
	database *db.DB
	sealer   crypto.Sealer
	adapters map[string]port.ProtocolRefresher
	settings config.Refresh
	now      func() time.Time
}

type refreshInvocation struct {
	RunID, TenantID, CredentialID, ShopID, OwnerUserID int64
	ActorID                                            *int64
	MaintenanceGeneration                              *int64
}

func newCredentialRefreshRuntime(database *db.DB, cfg *config.Config) (*credentialRefreshRuntime, error) {
	sealer, err := credentialSealer(cfg)
	if err != nil {
		return nil, err
	}
	return &credentialRefreshRuntime{database: database, sealer: sealer, now: time.Now,
		settings: cfg.Refresh, adapters: map[string]port.ProtocolRefresher{"mock": mock.New("")}}, nil
}

func (r *credentialRefreshRuntime) executor(guarded bool) (*refresh.Executor, error) {
	if r.sealer == nil {
		return nil, errs.New(30501, 500, "凭据刷新加密配置不可用")
	}
	raw := &credentialRefreshStore{database: r.database}
	var store refresh.Store = raw
	if guarded {
		store = &authorizedRefreshStore{credentialRefreshStore: raw, runtime: r}
	}
	return refresh.New(store, r.sealer, r, refresh.DefaultConfig(), r.now)
}

func (r *credentialRefreshRuntime) adapter(ctx context.Context, tenant, credentialID int64) (port.ProtocolRefresher, error) {
	q, cancel := r.database.WithContext(ctx)
	defer cancel()
	var credential struct{ Code, Kind string }
	err := q.Table("shop_credential c").Select("p.code,c.kind").Joins("JOIN shop s ON s.tenant_id=c.tenant_id AND s.id=c.shop_id").
		Joins("JOIN platform p ON p.id=s.platform_id AND p.status='active'").Where("c.tenant_id=? AND c.id=?", tenant, credentialID).Take(&credential).Error
	if err != nil {
		return nil, credentialDBError(err)
	}
	if credential.Kind == "cookie" {
		return nil, errs.New(30401, 400, "此凭据类型仅支持到期预警")
	}
	a, ok := r.adapters[credential.Code]
	if !ok {
		return nil, errs.New(30401, 400, "刷新协议待核验")
	}
	return a, nil
}

// Refresh is reached only after the capability and durable send barrier have committed.
func (r *credentialRefreshRuntime) Refresh(ctx context.Context, request port.Request) (port.Result, error) {
	a, err := scope.CredentialRefreshFrom(ctx)
	if err != nil || a.TenantID() != request.TenantID || a.CredentialID() != request.CredentialID || a.Generation() != request.Generation || a.InputVersionID() != request.InputVersionID {
		return port.Result{}, scope.Denied()
	}
	adapter, err := r.adapter(ctx, request.TenantID, request.CredentialID)
	if err != nil {
		return port.Result{}, err
	}
	return adapter.Refresh(ctx, request)
}

func (r *credentialRefreshRuntime) prepare(ctx context.Context, tenant, credentialID int64, trigger string, actor *int64) (model.RefreshRun, error) {
	var run model.RefreshRun
	if tenant <= 0 || credentialID <= 0 || (trigger != "manual" && trigger != "schedule" && trigger != "on_demand") {
		return run, scope.Denied()
	}
	core, err := r.executor(false)
	if err != nil {
		return run, err
	}
	err = r.database.Transaction(ctx, func(tx context.Context) error {
		raw := &credentialRefreshStore{database: r.database}
		snap, err := raw.Lock(tx, refresh.Target{TenantID: tenant, CredentialID: credentialID})
		if err != nil {
			return err
		}
		if snap.Credential.CurrentVersionID == nil {
			return refresh.ErrIneligible
		}
		inv := refreshInvocation{TenantID: tenant, CredentialID: credentialID, ShopID: snap.Shop.ID, ActorID: actor}
		if err := r.authorizeInvocation(tx, &inv, trigger, true); err != nil {
			return err
		}
		run, err = core.Prepare(tx, refresh.Target{TenantID: tenant, CredentialID: credentialID, InputVersionID: *snap.Credential.CurrentVersionID})
		if err != nil {
			return err
		}
		inv.RunID = run.ID
		q, cancel := r.database.WithContext(tx)
		defer cancel()
		insert := q.Table("credential_refresh_invocation").Clauses(clause.OnConflict{DoNothing: true}).Create(&inv)
		if insert.Error != nil {
			return credentialDBError(insert.Error)
		}
		if insert.RowsAffected == 1 {
			if err := q.Table("credential_refresh_run").Where("tenant_id=? AND id=?", tenant, run.ID).Update("trigger", trigger).Error; err != nil {
				return err
			}
			run.Trigger = trigger
		}
		if run.Status != "running" {
			return nil
		}
		payload, err := json.Marshal(struct {
			RunID int64 `json:"run_id"`
		}{run.ID})
		if err != nil {
			return err
		}
		return outbox.Emit(tx, r.database, outbox.Event{TenantID: tenant, AggregateType: "credential_refresh_run", AggregateID: strconv.FormatInt(run.ID, 10), EventType: "credential.refresh.dispatch", Payload: payload})
	})
	return run, err
}

func (r *credentialRefreshRuntime) loadRun(ctx context.Context, id int64) (model.RefreshRun, refreshInvocation, error) {
	var run model.RefreshRun
	var inv refreshInvocation
	if id <= 0 {
		return run, inv, scope.Denied()
	}
	q, cancel := r.database.WithContext(ctx)
	defer cancel()
	err := q.Table("credential_refresh_invocation").Select("run_id,tenant_id,credential_id,shop_id,owner_user_id,actor_id,maintenance_generation").Where("run_id=?", id).Take(&inv).Error
	if err != nil {
		return run, inv, credentialDBError(err)
	}
	err = q.Select(refreshRunColumns).Where("tenant_id=? AND credential_id=? AND id=?", inv.TenantID, inv.CredentialID, id).Take(&run).Error
	return run, inv, credentialDBError(err)
}

func (r *credentialRefreshRuntime) handle(ctx context.Context, id int64) error {
	run, inv, err := r.loadRun(ctx, id)
	if err != nil {
		return err
	}
	a, err := scope.IssueCredentialRefreshAccess(scope.BootstrapIssuer{}, inv.TenantID, inv.ShopID, inv.CredentialID, run.InputVersionID, run.ID, run.Generation, "credential.refresh")
	if err != nil {
		return err
	}
	core, err := r.executor(true)
	if err != nil {
		return err
	}
	_, err = core.Recover(scope.WithCredentialRefreshAccess(ctx, a), refresh.Target{TenantID: inv.TenantID, CredentialID: inv.CredentialID, InputVersionID: run.InputVersionID}, id)
	if errors.Is(err, refresh.ErrResultUnknown) || errors.Is(err, refresh.ErrIneligible) {
		return nil
	}
	return err
}
