package bootstrap

import (
	"context"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/shop/model"
	"github.com/omwk/go-admin-api/internal/modules/shop/refresh"
)

// Cleanup clears at most limit results and limit retired versions, in short transactions.
// Repeating it is safe; current versions and unexpired material are never cleared.
func (s *credentialRefreshStore) Cleanup(ctx context.Context, tenant int64, now time.Time, limit int) (refresh.CleanupResult, error) {
	var result refresh.CleanupResult
	if tenant <= 0 || now.IsZero() || limit <= 0 || limit > 500 {
		return result, refresh.ErrIneligible
	}
	q, cancel := s.database.WithContext(ctx)
	var stages []model.RefreshStage
	err := q.Select("id,tenant_id,credential_id,run_id").Where("tenant_id=? AND purged_at IS NULL AND purge_after<=?", tenant, now).
		Order("id").Limit(limit).Find(&stages).Error
	cancel()
	if err != nil {
		return result, credentialDBError(err)
	}
	for _, stage := range stages {
		changed, err := s.cleanupStage(ctx, stage, now)
		if err != nil {
			return result, err
		}
		if changed {
			result.Stages++
		}
	}
	var versions []model.CredentialVersion
	q, cancel = s.database.WithContext(ctx)
	defer cancel()
	err = q.Select("id,tenant_id,credential_id").Where(`tenant_id=? AND cipher_text IS NOT NULL AND retired_at<=?
        AND purge_after<=? AND NOT EXISTS (SELECT 1 FROM shop_credential c
        WHERE c.tenant_id=credential_version.tenant_id AND c.id=credential_version.credential_id AND c.current_version_id=credential_version.id)`,
		tenant, now.Add(-90*24*time.Hour), now).Order("id").Limit(limit).Find(&versions).Error
	if err != nil {
		return result, credentialDBError(err)
	}
	for _, version := range versions {
		changed, err := s.cleanupVersion(ctx, version, now)
		if err != nil {
			return result, err
		}
		if changed {
			result.Versions++
		}
	}
	return result, nil
}

func (s *credentialRefreshStore) cleanupStage(ctx context.Context, stage model.RefreshStage, now time.Time) (bool, error) {
	changed := false
	err := s.Transaction(ctx, func(tx context.Context) error {
		target := refresh.Target{TenantID: stage.TenantID, CredentialID: stage.CredentialID}
		if _, err := s.Lock(tx, target); err != nil {
			return err
		}
		run, err := s.FindRun(tx, target, stage.RunID)
		if err != nil {
			return err
		}
		if run == nil {
			return refresh.ErrIneligible
		}
		current, err := s.Stage(tx, *run)
		if err != nil {
			return err
		}
		if current.PurgedAt != nil || current.PurgeAfter.After(now) {
			return nil
		}
		if run.Phase == refresh.PhaseConsumed {
			run.Phase, run.FailureClass, run.Reason, run.UpdatedAt = refresh.PhaseUnknown, refresh.FailureUnknown, "stage_expired", now
			if err := s.SaveRun(tx, *run); err != nil {
				return err
			}
			if err := s.Reconcile(tx, *run, now); err != nil {
				return err
			}
		}
		if err := s.PurgeStage(tx, current, now); err != nil {
			return err
		}
		changed = true
		return s.Audit(tx, *run, "purge", "success", now)
	})
	return changed, err
}

func (s *credentialRefreshStore) cleanupVersion(ctx context.Context, version model.CredentialVersion, now time.Time) (bool, error) {
	changed := false
	err := s.Transaction(ctx, func(tx context.Context) error {
		target := refresh.Target{TenantID: version.TenantID, CredentialID: version.CredentialID}
		snap, err := s.Lock(tx, target)
		if err != nil {
			return err
		}
		if snap.Credential.CurrentVersionID != nil && *snap.Credential.CurrentVersionID == version.ID {
			return nil
		}
		q, cancel := s.database.WithContext(tx)
		defer cancel()
		result := q.Model(&model.CredentialVersion{}).Where(`tenant_id=? AND credential_id=? AND id=?
            AND retired_at<=? AND purge_after<=? AND cipher_text IS NOT NULL`, version.TenantID, version.CredentialID, version.ID, now.Add(-90*24*time.Hour), now).
			Updates(map[string]any{"cipher_text": nil, "nonce": nil})
		if result.Error != nil {
			return credentialDBError(result.Error)
		}
		if result.RowsAffected == 0 {
			return nil
		}
		changed = true
		return s.Audit(tx, model.RefreshRun{TenantID: version.TenantID, CredentialID: version.CredentialID}, "purge", "success", now)
	})
	return changed, err
}

// KeyReferences inventories every remaining credential ciphertext, regardless of age.
func (s *credentialRefreshStore) KeyReferences(ctx context.Context, tenant int64) ([]refresh.KeyReference, error) {
	if tenant <= 0 {
		return nil, refresh.ErrIneligible
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	rows := []refresh.KeyReference{}
	err := q.Raw(`SELECT key_version,sum(versions) AS versions,sum(stages) AS stages FROM (
        SELECT key_version,count(*) AS versions,0 AS stages FROM credential_version
        WHERE tenant_id=? AND cipher_text IS NOT NULL GROUP BY key_version UNION ALL
        SELECT key_version,0,count(*) FROM credential_refresh_stage
        WHERE tenant_id=? AND cipher_text IS NOT NULL GROUP BY key_version
        ) refs GROUP BY key_version ORDER BY key_version`, tenant, tenant).Scan(&rows).Error
	return rows, credentialDBError(err)
}
