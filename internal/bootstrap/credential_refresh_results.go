package bootstrap

import (
	"context"
	"encoding/json"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/shop/model"
	"gorm.io/gorm/clause"
)

func (s *credentialRefreshStore) InsertStage(ctx context.Context, stage *model.RefreshStage) error {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	return credentialDBError(q.Create(stage).Error)
}

func (s *credentialRefreshStore) Stage(ctx context.Context, run model.RefreshRun) (model.RefreshStage, error) {
	var stage model.RefreshStage
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	err := q.Select(refreshStageColumns).Where("tenant_id=? AND credential_id=? AND run_id=? AND id=?",
		run.TenantID, run.CredentialID, run.ID, run.StagedResultID).Take(&stage).Error
	return stage, credentialDBError(err)
}

func (s *credentialRefreshStore) PublishVersion(ctx context.Context, version *model.CredentialVersion, input int64, now time.Time) error {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	result := q.Model(&model.CredentialVersion{}).Where("tenant_id=? AND credential_id=? AND id=? AND retired_at IS NULL",
		version.TenantID, version.CredentialID, input).Updates(map[string]any{"retired_at": now, "purge_after": now.Add(90 * 24 * time.Hour)})
	if err := refreshAffected(result); err != nil {
		return err
	}
	return credentialDBError(q.Create(version).Error)
}

func (s *credentialRefreshStore) Audit(ctx context.Context, run model.RefreshRun, action, result string, now time.Time) error {
	detail, err := json.Marshal(struct {
		RunID        int64  `json:"run_id"`
		Generation   int64  `json:"generation"`
		Attempt      int    `json:"attempt"`
		FailureClass string `json:"failure_class,omitempty"`
	}{run.ID, run.Generation, run.Attempt, run.FailureClass})
	if err != nil {
		return err
	}
	trace := run.TraceID
	if trace == "" {
		trace = "credential-refresh"
	}
	audit := model.CredentialAudit{TenantID: run.TenantID, CredentialID: run.CredentialID, Action: action,
		ActorType: "system", Result: result, Detail: detail, TraceID: trace, CreatedAt: now}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	return credentialDBError(q.Create(&audit).Error)
}

func (s *credentialRefreshStore) Reconcile(ctx context.Context, run model.RefreshRun, now time.Time) error {
	record := model.RefreshReconciliation{TenantID: run.TenantID, CredentialID: run.CredentialID, RunID: run.ID, Reason: run.Reason, CreatedAt: now}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	return credentialDBError(q.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "tenant_id"}, {Name: "credential_id"}, {Name: "run_id"}}, DoNothing: true}).Create(&record).Error)
}

func (s *credentialRefreshStore) Resolve(ctx context.Context, run model.RefreshRun, now time.Time) error {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	return credentialDBError(q.Model(&model.RefreshReconciliation{}).
		Where("tenant_id=? AND credential_id=? AND run_id=? AND resolved_at IS NULL", run.TenantID, run.CredentialID, run.ID).
		Update("resolved_at", now).Error)
}

func (s *credentialRefreshStore) PurgeStage(ctx context.Context, stage model.RefreshStage, now time.Time) error {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	return refreshAffected(q.Model(&model.RefreshStage{}).Where("tenant_id=? AND credential_id=? AND id=? AND purged_at IS NULL",
		stage.TenantID, stage.CredentialID, stage.ID).Updates(map[string]any{"cipher_text": nil, "nonce": nil, "purged_at": now}))
}
