package bootstrap

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/outbox"
	"github.com/omwk/go-admin-api/internal/kernel/scope"
	"github.com/omwk/go-admin-api/internal/modules/shop/model"
	"github.com/omwk/go-admin-api/internal/modules/shop/refresh"
)

type authorizedRefreshStore struct {
	*credentialRefreshStore
	runtime *credentialRefreshRuntime
}

func (s *authorizedRefreshStore) Lock(ctx context.Context, t refresh.Target) (refresh.Snapshot, error) {
	a, err := scope.CredentialRefreshFrom(ctx)
	if err != nil || a.TenantID() != t.TenantID || a.CredentialID() != t.CredentialID || a.InputVersionID() != t.InputVersionID {
		return refresh.Snapshot{}, scope.Denied()
	}
	snap, err := s.credentialRefreshStore.Lock(ctx, t)
	if err == nil && snap.Shop.ID != a.ShopID() {
		return refresh.Snapshot{}, scope.Denied()
	}
	return snap, err
}

func (s *authorizedRefreshStore) FindRun(ctx context.Context, t refresh.Target, id int64) (*model.RefreshRun, error) {
	a, err := scope.CredentialRefreshFrom(ctx)
	if err != nil || id != a.RunID() {
		return nil, scope.Denied()
	}
	run, err := s.credentialRefreshStore.FindRun(ctx, t, id)
	if err != nil {
		return nil, err
	}
	if run == nil || run.Generation != a.Generation() || run.InputVersionID != a.InputVersionID() {
		return nil, scope.Denied()
	}
	if run.Phase == refresh.PhaseIntent && run.Status == "running" {
		_, inv, err := s.runtime.loadRun(ctx, id)
		if err != nil {
			return nil, err
		}
		if err := s.runtime.authorizeInvocation(ctx, &inv, run.Trigger, false); err != nil {
			return nil, err
		}
	}
	return run, nil
}

func (s *authorizedRefreshStore) PublishVersion(ctx context.Context, v *model.CredentialVersion, input int64, now time.Time) error {
	a, err := scope.CredentialRefreshFrom(ctx)
	if err != nil || v.TenantID != a.TenantID() || v.CredentialID != a.CredentialID() || input != a.InputVersionID() {
		return scope.Denied()
	}
	run, inv, err := s.runtime.loadRun(ctx, a.RunID())
	if err != nil {
		return err
	}
	if err := s.runtime.authorizeInvocation(ctx, &inv, run.Trigger, false); err != nil {
		return err
	}
	return s.credentialRefreshStore.PublishVersion(ctx, v, input, now)
}

func (s *authorizedRefreshStore) SaveRun(ctx context.Context, run model.RefreshRun) error {
	if err := s.credentialRefreshStore.SaveRun(ctx, run); err != nil {
		return err
	}
	if run.Phase == refresh.PhasePublished && run.OutputVersionID != nil {
		if err := emitCredentialAvailable(ctx, s.database, run.TenantID, run.CredentialID, *run.OutputVersionID); err != nil {
			return err
		}
	}
	if run.Phase != refresh.PhaseUnknown && run.Status != "failed" && run.Status != "success" {
		return nil
	}
	payload, err := json.Marshal(struct {
		RunID int64 `json:"run_id"`
	}{run.ID})
	if err != nil {
		return err
	}
	return outbox.Emit(ctx, s.database, outbox.Event{TenantID: run.TenantID, AggregateType: "credential_refresh_run", AggregateID: strconv.FormatInt(run.ID, 10), EventType: "credential.refresh.observed", Payload: payload})
}
