package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/kernel/outbox"
)

func emitCredentialAvailable(ctx context.Context, database *db.DB, tenant, credentialID, version int64) error {
	payload, err := json.Marshal(struct {
		CredentialID int64 `json:"credential_id"`
		VersionID    int64 `json:"version_id"`
	}{credentialID, version})
	if err != nil {
		return err
	}
	return outbox.Emit(ctx, database, outbox.Event{TenantID: tenant, AggregateType: "credential", AggregateID: strconv.FormatInt(credentialID, 10), EventType: "credential.available", Payload: payload})
}

func (r *credentialRefreshRuntime) events(fallback func(context.Context, outbox.Event) error) func(context.Context, outbox.Event) error {
	return func(ctx context.Context, event outbox.Event) error {
		switch event.EventType {
		case "credential.available":
			if err := fallback(ctx, event); err != nil {
				return err
			}
			return newCredentialNotifications(r).recovered(ctx, event)
		case "credential.refresh.observed":
			return newCredentialNotifications(r).observe(ctx)
		case "credential.refresh.dispatch":
			var payload struct {
				RunID int64 `json:"run_id"`
			}
			if json.Unmarshal(event.Payload, &payload) != nil || payload.RunID <= 0 || event.AggregateType != "credential_refresh_run" || event.AggregateID != strconv.FormatInt(payload.RunID, 10) {
				return errors.New("invalid credential refresh dispatch")
			}
			run, _, err := r.loadRun(ctx, payload.RunID)
			if err != nil {
				return err
			}
			if run.TenantID != event.TenantID {
				return errors.New("credential refresh tenant mismatch")
			}
			return r.handle(ctx, payload.RunID)
		case "credential.refresh.requested":
			var payload struct {
				CredentialID int64 `json:"credential_id"`
			}
			if json.Unmarshal(event.Payload, &payload) != nil || payload.CredentialID <= 0 || event.AggregateType != "credential" || event.AggregateID != strconv.FormatInt(payload.CredentialID, 10) {
				return errors.New("invalid credential refresh request")
			}
			_, err := r.prepare(ctx, event.TenantID, payload.CredentialID, "on_demand", nil)
			if refreshIneligible(err) {
				return nil
			}
			return err
		default:
			return fallback(ctx, event)
		}
	}
}
