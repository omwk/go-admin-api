package bootstrap

import (
	"context"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/shop/model"
	"github.com/omwk/go-admin-api/internal/modules/shop/repo"
)

func (s *shopStore) CreateCredential(ctx context.Context, tenant, shopID int64, row *model.Credential, build func(int64, int) (*model.CredentialVersion, error), audit model.CredentialAudit) (repo.CredentialRow, error) {
	if err := s.lockCredentialShop(ctx, tenant, shopID, "CreateCredential"); err != nil {
		return repo.CredentialRow{}, err
	}
	if row.TenantID != tenant || row.ShopID != shopID {
		return repo.CredentialRow{}, errs.New(errs.ErrInternal, 500, "凭据关系无效")
	}
	q, cancel := s.raw(ctx)
	defer cancel()
	if err := q.Create(row).Error; err != nil {
		return repo.CredentialRow{}, credentialDBError(err)
	}
	version, err := build(row.ID, 1)
	if err != nil {
		return repo.CredentialRow{}, err
	}
	if err := validBuiltVersion(version, *row, 1); err != nil {
		return repo.CredentialRow{}, err
	}
	if err := s.insertCredentialVersion(ctx, version); err != nil {
		return repo.CredentialRow{}, err
	}
	row.CurrentVersionID = &version.ID
	if err := s.updateCredential(ctx, *row, nil); err != nil {
		return repo.CredentialRow{}, err
	}
	result := repo.CredentialRow{Credential: *row, VersionNo: 1, Fingerprint: version.Fingerprint}
	if err := s.recordCredentialMaintenance(ctx, *row, audit); err != nil {
		return repo.CredentialRow{}, err
	}
	return result, s.appendCredentialAudit(ctx, audit, result)
}

func (s *shopStore) RotateCredential(ctx context.Context, tenant, id int64, build func(model.Credential, int) (*model.CredentialVersion, model.Credential, error), audit model.CredentialAudit) (repo.CredentialRow, error) {
	current, err := s.lockedCredential(ctx, tenant, id, "RotateCredential")
	if err != nil {
		return repo.CredentialRow{}, err
	}
	if err := s.fenceCredentialRefresh(ctx, &current.Credential); err != nil {
		return repo.CredentialRow{}, err
	}
	version, next, err := build(current.Credential, current.VersionNo+1)
	if err != nil {
		return repo.CredentialRow{}, err
	}
	if err := validBuiltVersion(version, current.Credential, current.VersionNo+1); err != nil {
		return repo.CredentialRow{}, err
	}
	if err := s.retireCredentialVersion(ctx, current); err != nil {
		return repo.CredentialRow{}, err
	}
	if err := s.insertCredentialVersion(ctx, version); err != nil {
		return repo.CredentialRow{}, err
	}
	next.CurrentVersionID = &version.ID
	if err := s.updateCredential(ctx, next, current.CurrentVersionID); err != nil {
		return repo.CredentialRow{}, err
	}
	result := repo.CredentialRow{Credential: next, VersionNo: version.VersionNo, Fingerprint: version.Fingerprint}
	if err := s.recordCredentialMaintenance(ctx, next, audit); err != nil {
		return repo.CredentialRow{}, err
	}
	if err := emitCredentialAvailable(ctx, s.database, tenant, id, version.ID); err != nil {
		return repo.CredentialRow{}, err
	}
	return result, s.appendCredentialAudit(ctx, audit, result)
}

func (s *shopStore) DisableCredential(ctx context.Context, tenant, id int64, audit model.CredentialAudit) (repo.CredentialRow, error) {
	row, err := s.lockedCredential(ctx, tenant, id, "DisableCredential")
	if err != nil {
		return repo.CredentialRow{}, err
	}
	row.Status = "disabled"
	if err := s.fenceCredentialRefresh(ctx, &row.Credential); err != nil {
		return repo.CredentialRow{}, err
	}
	if err := s.updateCredential(ctx, row.Credential, row.CurrentVersionID); err != nil {
		return repo.CredentialRow{}, err
	}
	if err := s.recordCredentialMaintenance(ctx, row.Credential, audit); err != nil {
		return repo.CredentialRow{}, err
	}
	return row, s.appendCredentialAudit(ctx, audit, row)
}

func (s *shopStore) retireCredentialVersion(ctx context.Context, current repo.CredentialRow) error {
	q, cancel := s.raw(ctx)
	defer cancel()
	now := time.Now().UTC()
	result := q.Model(&model.CredentialVersion{}).
		Where("tenant_id = ? AND credential_id = ? AND id = ? AND retired_at IS NULL", current.TenantID, current.ID, current.CurrentVersionID).
		Updates(map[string]any{"retired_at": now, "purge_after": now.Add(90 * 24 * time.Hour)})
	if result.Error != nil {
		return credentialDBError(result.Error)
	}
	if result.RowsAffected != 1 {
		return credentialVersionConflict()
	}
	return nil
}

func validBuiltVersion(version *model.CredentialVersion, row model.Credential, number int) error {
	if version == nil || version.TenantID != row.TenantID || version.CredentialID != row.ID || version.VersionNo != number {
		return errs.New(errs.ErrInternal, 500, "凭据版本关系无效")
	}
	return nil
}
