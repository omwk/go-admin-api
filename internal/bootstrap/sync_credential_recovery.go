package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/kernel/outbox"
	"github.com/omwk/go-admin-api/internal/kernel/scope"
	"github.com/omwk/go-admin-api/internal/modules/sync/repo"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *syncEngineStore) blockCredential(ctx context.Context, q *gorm.DB, run repo.Run, a scope.WorkAccess) error {
	var credential struct {
		ID               int64
		CurrentVersionID *int64
	}
	err := q.Raw(`SELECT c.id,c.current_version_id FROM shop_credential c
 JOIN sync_run r ON r.tenant_id=c.tenant_id AND r.shop_id=c.shop_id AND r.id=? AND r.job_id=?
 WHERE c.tenant_id=? AND c.shop_id=? AND c.current_version_id IS NOT NULL
 ORDER BY (c.id=r.credential_used_id) DESC NULLS LAST,c.id LIMIT 1`, a.RunID(), a.JobID(), a.TenantID(), a.ShopID()).Take(&credential).Error
	if err != nil {
		return credentialDBError(err)
	}
	run.Status, run.ErrorCode, run.ErrorMsg = "partial", "credential_expired", "waiting for credential maintenance"
	run.Stats.Sequence++
	run.Stats.Dispatch++
	if err := saveSyncRun(q, run); err != nil {
		return err
	}
	if err := q.Table("sync_run").Where("tenant_id=? AND id=?", a.TenantID(), a.RunID()).Update("credential_blocked_id", credential.ID).Error; err != nil {
		return err
	}
	if err := q.Table("sync_job").Where("tenant_id=? AND id=?", a.TenantID(), a.JobID()).Updates(map[string]any{
		"credential_blocked_id": credential.ID, "credential_blocked_version": credential.CurrentVersionID, "credential_blocked_at": s.now().UTC()}).Error; err != nil {
		return err
	}
	payload, err := json.Marshal(struct {
		CredentialID int64 `json:"credential_id"`
	}{credential.ID})
	if err != nil {
		return err
	}
	return outbox.Emit(ctx, s.database, outbox.Event{TenantID: a.TenantID(), AggregateType: "credential", AggregateID: strconv.FormatInt(credential.ID, 10), EventType: "credential.refresh.requested", Payload: payload})
}

func credentialAvailableEventHandler(database *db.DB, fallback func(context.Context, outbox.Event) error) func(context.Context, outbox.Event) error {
	return func(ctx context.Context, event outbox.Event) error {
		if event.EventType != "credential.available" {
			return fallback(ctx, event)
		}
		var payload struct {
			CredentialID int64 `json:"credential_id"`
			VersionID    int64 `json:"version_id"`
		}
		if json.Unmarshal(event.Payload, &payload) != nil || payload.CredentialID <= 0 || payload.VersionID <= 0 || event.TenantID <= 0 || event.AggregateType != "credential" || event.AggregateID != strconv.FormatInt(payload.CredentialID, 10) {
			return errors.New("invalid credential availability identifiers")
		}
		return recoverCredentialJobs(ctx, database, event.TenantID, payload.CredentialID, payload.VersionID)
	}
}

func recoverCredentialJobs(ctx context.Context, database *db.DB, tenant, credentialID, version int64) error {
	return database.Transaction(ctx, func(tx context.Context) error {
		q, cancel := database.WithContext(tx)
		defer cancel()
		var shopID int64
		err := q.Table("shop_credential c").Select("c.shop_id").Joins("JOIN shop s ON s.tenant_id=c.tenant_id AND s.id=c.shop_id").
			Where("c.tenant_id=? AND c.id=? AND c.current_version_id=? AND c.status IN ('active','expiring') AND (c.expires_at IS NULL OR c.expires_at>?) AND s.status='active' AND s.deleted_at IS NULL", tenant, credentialID, version, time.Now().UTC()).Take(&shopID).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		var jobs []int64
		if err := q.Table("sync_job").Select("id").Where("tenant_id=? AND shop_id=? AND enabled AND credential_blocked_id=? AND credential_blocked_version<>?", tenant, shopID, credentialID, version).
			Order("id").Clauses(clause.Locking{Strength: "UPDATE"}).Find(&jobs).Error; err != nil {
			return err
		}
		store := newSyncEngineStore(database, registeredResourceHandlers(nil), config.Sync{})
		for _, job := range jobs {
			if err := q.Table("sync_job").Where("tenant_id=? AND id=? AND enabled AND credential_blocked_id=?", tenant, job, credentialID).
				Updates(map[string]any{"credential_blocked_id": nil, "credential_blocked_version": nil, "credential_blocked_at": nil}).Error; err != nil {
				return err
			}
			if err := wakeCredentialRuns(tx, q, store, tenant, job, credentialID); err != nil {
				return err
			}
		}
		return nil
	})
}

func wakeCredentialRuns(ctx context.Context, q *gorm.DB, store *syncEngineStore, tenant, jobID, credentialID int64) error {
	var runs []repo.Run
	if err := q.Table("sync_run").Select(syncRunColumns).Where("tenant_id=? AND job_id=? AND credential_blocked_id=? AND status='partial' AND finished_at IS NULL AND stats->>'pending'='true'", tenant, jobID, credentialID).
		Order("id").Clauses(clause.Locking{Strength: "UPDATE"}).Find(&runs).Error; err != nil {
		return err
	}
	for _, run := range runs {
		if _, err := store.LoadWork(ctx, jobID, run.ID); err != nil {
			if errors.Is(err, errRunAuthorizationRevoked) {
				if err := store.cancelUnauthorized(ctx, run); err != nil {
					return err
				}
				continue
			}
			return err
		}
		run.ErrorCode, run.ErrorMsg = "", ""
		run.Stats.Dispatch++
		if err := saveSyncRun(q, run); err != nil {
			return err
		}
		if err := q.Table("sync_run").Where("tenant_id=? AND id=?", tenant, run.ID).Update("credential_blocked_id", nil).Error; err != nil {
			return err
		}
		if err := store.emitRun(ctx, run, 0); err != nil {
			return err
		}
	}
	return nil
}
