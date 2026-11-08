package refresh

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/crypto"
	"github.com/omwk/go-admin-api/internal/modules/shop/model"
	"github.com/omwk/go-admin-api/internal/modules/shop/refresh/port"
)

func validResult(result port.Result, now time.Time) bool {
	var values map[string]json.RawMessage
	err := json.Unmarshal(result.Values, &values)
	defer func() {
		for _, v := range values {
			clear(v)
		}
	}()
	for k, v := range values {
		if strings.TrimSpace(k) == "" || strings.ContainsAny(k, "\r\n") || bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return false
		}
	}
	return err == nil && len(values) > 0 && result.ExpiresAt.After(now) && result.RefreshAt.After(now) && !result.RefreshAt.After(result.ExpiresAt)
}

func (e *Executor) finishResult(ctx context.Context, target Target, run model.RefreshRun, snap Snapshot, result port.Result) (model.RefreshRun, error) {
	if !validResult(result, e.now()) {
		return e.unknown(ctx, target, run.ID, "ambiguous")
	}
	aad, err := (crypto.CredentialAAD{TenantID: run.TenantID, ShopID: snap.Shop.ID, CredentialID: run.CredentialID,
		Kind: snap.Credential.Kind, VersionNo: run.CandidateVersion}).Encode()
	if err != nil {
		return e.unknown(ctx, target, run.ID, "stage_failed")
	}
	ciphertext, nonce, key, err := e.sealer.Seal(result.Values, aad)
	if err != nil {
		return e.unknown(ctx, target, run.ID, "stage_failed")
	}
	now := e.now().UTC()
	stage := model.RefreshStage{TenantID: run.TenantID, CredentialID: run.CredentialID, RunID: run.ID, VersionNo: run.CandidateVersion,
		CipherText: ciphertext, Nonce: nonce, KeyVersion: key, AAD: aad, Fingerprint: crypto.Fingerprint(result.Values),
		ExpiresAt: result.ExpiresAt.UTC(), RefreshAt: result.RefreshAt.UTC(), CreatedAt: now, PurgeAfter: now.Add(24 * time.Hour)}
	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), e.config.FinishTimeout)
	defer cancel()
	// Retry only the same encrypted result, including an ambiguous local commit.
	runID := run.ID
	for attempt := 0; attempt < 2; attempt++ {
		run, err = e.withRun(finish, target, runID, func(tx context.Context, _ Snapshot, r *model.RefreshRun) error {
			if r.Phase == PhaseConsumed || r.Phase == PhasePublished {
				return nil
			}
			if r.Phase != PhaseSending && r.Phase != PhaseUnknown {
				return ErrIneligible
			}
			if err := e.store.InsertStage(tx, &stage); err != nil {
				return err
			}
			stageID := stage.ID
			r.Phase, r.StagedResultID, r.FailureClass, r.Reason, r.UpdatedAt = PhaseConsumed, &stageID, "", "", e.now().UTC()
			if err := e.store.SaveRun(tx, *r); err != nil {
				return err
			}
			return e.store.Resolve(tx, *r, e.now().UTC())
		})
		if err == nil {
			return e.Publish(finish, target, run.ID)
		}
		stage.ID = 0
	}
	return e.unknown(ctx, target, runID, "stage_failed")
}

// Publish restores a staged result locally; it never calls the platform.
func (e *Executor) Publish(ctx context.Context, target Target, id int64) (model.RefreshRun, error) {
	return e.withRun(ctx, target, id, func(tx context.Context, snap Snapshot, run *model.RefreshRun) error {
		if run.Status != "running" || run.Phase != PhaseConsumed {
			return nil
		}
		if !eligible(snap, *run) {
			return e.supersede(tx, run)
		}
		stage, err := e.store.Stage(tx, *run)
		if err != nil {
			return err
		}
		now := e.now().UTC()
		if stage.PurgedAt != nil || !stage.PurgeAfter.After(now) || !stage.ExpiresAt.After(now) {
			return e.markUnknown(tx, run, "stage_expired")
		}
		if err := e.verifyStage(snap, *run, stage); err != nil {
			return err
		}
		version := model.CredentialVersion{TenantID: run.TenantID, CredentialID: run.CredentialID, VersionNo: run.CandidateVersion,
			CipherText: stage.CipherText, Nonce: stage.Nonce, KeyVersion: stage.KeyVersion, AAD: stage.AAD, Fingerprint: stage.Fingerprint,
			IssuedAt: stage.CreatedAt, ExpiresAt: &stage.ExpiresAt, CreatedReason: "refresh", RawMeta: json.RawMessage(`{}`)}
		if err := e.store.PublishVersion(tx, &version, run.InputVersionID, now); err != nil {
			return err
		}
		c := snap.Credential
		c.CurrentVersionID, c.ExpiresAt, c.RefreshAt, c.LastRefreshedAt = &version.ID, &stage.ExpiresAt, &stage.RefreshAt, &now
		c.Status, c.ConsecutiveFailures, c.RetryBudget = ProjectStatus(now, c.ExpiresAt), 0, MaxAttempts
		if err := e.store.SaveCredential(tx, c); err != nil {
			return err
		}
		run.Phase, run.Status, run.OutputVersionID, run.FinishedAt, run.UpdatedAt = PhasePublished, "success", &version.ID, &now, now
		if err := e.store.SaveRun(tx, *run); err != nil {
			return err
		}
		if err := e.store.PurgeStage(tx, stage, now); err != nil {
			return err
		}
		if err := e.store.Audit(tx, *run, "refresh", "success", now); err != nil {
			return err
		}
		return e.store.Resolve(tx, *run, now)
	})
}

func (e *Executor) verifyStage(snap Snapshot, run model.RefreshRun, stage model.RefreshStage) error {
	aad, err := (crypto.CredentialAAD{TenantID: run.TenantID, ShopID: snap.Shop.ID, CredentialID: run.CredentialID,
		Kind: snap.Credential.Kind, VersionNo: run.CandidateVersion}).Encode()
	if err != nil || stage.TenantID != run.TenantID || stage.CredentialID != run.CredentialID || stage.RunID != run.ID ||
		stage.VersionNo != run.CandidateVersion || stage.AAD != aad {
		return crypto.DecryptionFailed()
	}
	plain, err := e.sealer.Open(stage.CipherText, stage.Nonce, aad, stage.KeyVersion)
	clear(plain)
	if err != nil {
		return crypto.DecryptionFailed()
	}
	return nil
}
