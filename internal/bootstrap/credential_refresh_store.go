package bootstrap

import (
	"context"
	"errors"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/modules/shop/model"
	"github.com/omwk/go-admin-api/internal/modules/shop/refresh"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// credentialRefreshStore is deliberately not wired to API, worker, or WorkAccess.
// Its caller supplies an already authorized target; M7b owns that capability boundary.
type credentialRefreshStore struct{ database *db.DB }

var _ refresh.Store = (*credentialRefreshStore)(nil)

const refreshStateColumns = `id,tenant_id,shop_id,kind,label,status,current_version_id,expires_at,refresh_at,
last_checked_at,last_refreshed_at,refresh_strategy,consecutive_failures,retry_budget,refresh_generation,extra,created_at,updated_at`
const refreshVersionColumns = `id,tenant_id,credential_id,version_no,cipher_text,nonce,key_version,aad,fingerprint,
issued_at,expires_at,created_reason,created_by,raw_meta,retired_at,purge_after`
const refreshRunColumns = `id,tenant_id,credential_id,input_version_id,generation,candidate_version,staged_result_id,
output_version_id,phase,status,failure_class,reason,attempt,trigger,send_deadline,next_retry_at,started_at,updated_at,finished_at,trace_id`
const refreshStageColumns = `id,tenant_id,credential_id,run_id,version_no,cipher_text,nonce,key_version,aad,fingerprint,
expires_at,refresh_at,created_at,purge_after,purged_at`

func (s *credentialRefreshStore) Transaction(ctx context.Context, fn func(context.Context) error) error {
	err := s.database.Transaction(ctx, fn)
	if errors.Is(err, refresh.ErrIneligible) || errors.Is(err, refresh.ErrResultUnknown) || errors.Is(err, refresh.ErrPersistence) {
		return err
	}
	return credentialDBError(err)
}

func (s *credentialRefreshStore) Lock(ctx context.Context, target refresh.Target) (refresh.Snapshot, error) {
	var snap refresh.Snapshot
	if !db.InTransaction(ctx) || target.TenantID <= 0 || target.CredentialID <= 0 {
		return snap, refresh.ErrIneligible
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	// Keep M2's shop -> credential order; no transaction survives an HTTP call.
	err := q.Unscoped().Model(&model.Shop{}).Select("shop.id,shop.tenant_id,shop.status,shop.deleted_at").
		Joins("JOIN shop_credential c ON c.tenant_id=shop.tenant_id AND c.shop_id=shop.id").
		Where("shop.tenant_id=? AND c.id=?", target.TenantID, target.CredentialID).
		Clauses(clause.Locking{Strength: "UPDATE", Table: clause.Table{Name: "shop"}}).Take(&snap.Shop).Error
	if err != nil {
		return snap, credentialDBError(err)
	}
	err = q.Select(refreshStateColumns).Where("tenant_id=? AND shop_id=? AND id=?", target.TenantID, snap.Shop.ID, target.CredentialID).
		Clauses(clause.Locking{Strength: "UPDATE"}).Take(&snap.Credential).Error
	if err != nil {
		return snap, credentialDBError(err)
	}
	if snap.Credential.CurrentVersionID == nil {
		return snap, refresh.ErrIneligible
	}
	err = q.Select(refreshVersionColumns).Where("tenant_id=? AND credential_id=? AND id=?", target.TenantID, target.CredentialID, *snap.Credential.CurrentVersionID).
		Take(&snap.Version).Error
	return snap, credentialDBError(err)
}

func (s *credentialRefreshStore) FindRun(ctx context.Context, target refresh.Target, id int64) (*model.RefreshRun, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	query := q.Select(refreshRunColumns).Where("tenant_id=? AND credential_id=?", target.TenantID, target.CredentialID)
	if id > 0 {
		query = query.Where("id=?", id)
	} else {
		query = query.Where("input_version_id=?", target.InputVersionID).Order("attempt DESC")
	}
	var run model.RefreshRun
	err := query.Take(&run).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, credentialDBError(err)
	}
	return &run, nil
}

func (s *credentialRefreshStore) InsertRun(ctx context.Context, run *model.RefreshRun) error {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	return credentialDBError(q.Create(run).Error)
}

func (s *credentialRefreshStore) SaveRun(ctx context.Context, run model.RefreshRun) error {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	return refreshAffected(q.Model(&model.RefreshRun{}).Where("tenant_id=? AND credential_id=? AND id=? AND generation=?",
		run.TenantID, run.CredentialID, run.ID, run.Generation).Updates(map[string]any{
		"phase": run.Phase, "status": run.Status, "failure_class": run.FailureClass, "reason": run.Reason,
		"staged_result_id": run.StagedResultID, "output_version_id": run.OutputVersionID, "send_deadline": run.SendDeadline,
		"next_retry_at": run.NextRetryAt, "updated_at": run.UpdatedAt, "finished_at": run.FinishedAt}))
}

func (s *credentialRefreshStore) SaveCredential(ctx context.Context, row model.Credential) error {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	return refreshAffected(q.Model(&model.Credential{}).Where("tenant_id=? AND shop_id=? AND id=?", row.TenantID, row.ShopID, row.ID).
		Updates(map[string]any{"current_version_id": row.CurrentVersionID, "status": row.Status, "expires_at": row.ExpiresAt,
			"refresh_at": row.RefreshAt, "last_refreshed_at": row.LastRefreshedAt, "consecutive_failures": row.ConsecutiveFailures,
			"retry_budget": row.RetryBudget, "refresh_generation": row.RefreshGeneration, "updated_at": time.Now().UTC()}))
}

func refreshAffected(result *gorm.DB) error {
	if result.Error != nil {
		return credentialDBError(result.Error)
	}
	if result.RowsAffected != 1 {
		return refresh.ErrIneligible
	}
	return nil
}
