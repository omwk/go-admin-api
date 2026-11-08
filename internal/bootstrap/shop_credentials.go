package bootstrap

import (
	"context"
	"errors"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/shop"
	"github.com/omwk/go-admin-api/internal/modules/shop/model"
	"github.com/omwk/go-admin-api/internal/modules/shop/repo"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var _ repo.CredentialStore = (*shopStore)(nil)
var _ repo.CredentialUseStore = (*shopStore)(nil)

const publicMetadataColumns = `shop_credential.id, shop_credential.tenant_id, shop_credential.shop_id,
shop_credential.kind, shop_credential.label, shop_credential.status, shop_credential.current_version_id,
shop_credential.expires_at, shop_credential.last_checked_at, shop_credential.refresh_strategy,
shop_credential.refresh_generation, shop_credential.retry_budget, shop_credential.consecutive_failures,
shop_credential.refresh_at, shop_credential.last_refreshed_at,
shop_credential.extra, shop_credential.created_at, shop_credential.updated_at, cv.version_no, cv.fingerprint`

// CredentialTransaction is invoked by the service so projection and audit share the commit boundary.
func (s *shopStore) CredentialTransaction(ctx context.Context, fn func(context.Context) error) error {
	return credentialDBError(s.database.Transaction(ctx, fn))
}

func (s *shopStore) WorkerCredential(ctx context.Context, tenant, shopID int64) (repo.WorkerCredential, error) {
	if tenant <= 0 || shopID <= 0 {
		return repo.WorkerCredential{}, errs.New(errs.ErrDataScopeDenied, 403, "超出数据权限")
	}
	if _, err := scopeRegistryQuery("CredentialUse.WorkerCredential"); err != nil {
		return repo.WorkerCredential{}, err
	}
	q, cancel := s.raw(ctx)
	defer cancel()
	var result repo.WorkerCredential
	if err := q.Model(&model.Shop{}).Where("tenant_id = ? AND id = ? AND status = ? AND deleted_at IS NULL", tenant, shopID, "active").Take(&result.Shop).Error; err != nil {
		return repo.WorkerCredential{}, credentialDBError(err)
	}
	now := time.Now().UTC()
	if err := q.Model(&model.Credential{}).Where("tenant_id = ? AND shop_id = ? AND status = ? AND current_version_id IS NOT NULL AND (expires_at IS NULL OR expires_at > ?)", tenant, shopID, "active", now).Order("id asc").Take(&result.Credential).Error; err != nil {
		return repo.WorkerCredential{}, credentialDBError(err)
	}
	if result.Credential.CurrentVersionID == nil {
		return repo.WorkerCredential{}, errs.New(errs.ErrNotFound, 404, "凭据不存在")
	}
	if err := q.Model(&model.CredentialVersion{}).Where("tenant_id = ? AND credential_id = ? AND id = ? AND retired_at IS NULL AND (expires_at IS NULL OR expires_at > ?)", tenant, result.Credential.ID, *result.Credential.CurrentVersionID, now).Take(&result.Version).Error; err != nil {
		return repo.WorkerCredential{}, credentialDBError(err)
	}
	return result, nil
}

func (s *shopStore) AuditCredentialUse(ctx context.Context, tenant, credentialID int64, audit model.CredentialAudit) error {
	if _, err := scopeRegistryQuery("CredentialUse.AuditCredentialUse"); err != nil {
		return err
	}
	audit.TenantID, audit.CredentialID, audit.ActorType = tenant, credentialID, "system"
	if audit.TraceID == "" {
		audit.TraceID = "credential-worker"
	}
	q, cancel := s.raw(ctx)
	defer cancel()
	return credentialDBError(q.Create(&audit).Error)
}

// ValidateCredential checks the live scoped parent without appending a read
// audit, which keeps idempotent replay side-effect free.
func (s *shopStore) ValidateCredential(ctx context.Context, tenant, id int64) error {
	_, err := s.Shop(ctx, tenant, id)
	return err
}

// credentialQuery always starts from the scoped, non-deleted shop root.
func (s *shopStore) credentialQuery(ctx context.Context, tenant int64, method string) *db.ScopedQuery {
	q, _ := s.query(ctx, tenant, &model.Shop{}, "Credential."+method)
	return q.Joins(`JOIN shop_credential ON shop_credential.tenant_id = shop.tenant_id AND shop_credential.shop_id = shop.id`).
		Joins(`JOIN credential_version cv ON cv.tenant_id = shop_credential.tenant_id AND cv.credential_id = shop_credential.id AND cv.id = shop_credential.current_version_id`)
}

func (s *shopStore) credentialParent(ctx context.Context, tenant, id int64, method string) (repo.CredentialRow, error) {
	q := s.credentialQuery(ctx, tenant, method)
	defer q.Close()
	var row repo.CredentialRow
	err := q.Select(publicMetadataColumns).Where("shop_credential.id = ?", id).Take(&row)
	return row, credentialDBError(err)
}

func (s *shopStore) lockCredentialShop(ctx context.Context, tenant, id int64, method string) error {
	q, cancel := s.query(ctx, tenant, &model.Shop{}, "Credential."+method)
	defer cancel()
	var row model.Shop
	return credentialDBError(q.Select("shop.id").Where("shop.id = ?", id).Lock().Take(&row))
}

func (s *shopStore) lockedCredential(ctx context.Context, tenant, id int64, method string) (repo.CredentialRow, error) {
	before, err := s.credentialParent(ctx, tenant, id, method)
	if err != nil {
		return repo.CredentialRow{}, err
	}
	if err := s.lockCredentialShop(ctx, tenant, before.ShopID, method); err != nil {
		return repo.CredentialRow{}, err
	}
	q, cancel := s.raw(ctx)
	defer cancel()
	var current model.Credential
	err = q.Select("id, tenant_id, shop_id, current_version_id").Where("tenant_id = ? AND shop_id = ? AND id = ?", tenant, before.ShopID, id).
		Clauses(clause.Locking{Strength: "UPDATE"}).Take(&current).Error
	if err != nil {
		return repo.CredentialRow{}, credentialDBError(err)
	}
	if current.CurrentVersionID == nil || before.CurrentVersionID == nil || *current.CurrentVersionID != *before.CurrentVersionID {
		return repo.CredentialRow{}, credentialVersionConflict()
	}
	// Reload metadata only after both locks: a concurrent disable must not be lost.
	return s.credentialParent(ctx, tenant, id, method)
}

func (s *shopStore) appendCredentialAudit(ctx context.Context, audit model.CredentialAudit, row repo.CredentialRow) error {
	audit.TenantID, audit.CredentialID = row.TenantID, row.ID
	q, cancel := s.raw(ctx)
	defer cancel()
	return credentialDBError(q.Create(&audit).Error)
}

func credentialDBError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return shop.NotFound()
	}
	var app *errs.AppError
	if errors.As(err, &app) {
		return app
	}
	// PostgreSQL errors may include submitted metadata in Detail; never retain that chain.
	return errs.New(errs.ErrInternal, 500, "凭据操作失败")
}

func (s *shopStore) insertCredentialVersion(ctx context.Context, version *model.CredentialVersion) error {
	q, cancel := s.raw(ctx)
	defer cancel()
	return credentialDBError(q.Create(version).Error)
}

func (s *shopStore) updateCredential(ctx context.Context, row model.Credential, expected *int64) error {
	q, cancel := s.raw(ctx)
	defer cancel()
	query := q.Model(&model.Credential{}).Where("tenant_id = ? AND shop_id = ? AND id = ?", row.TenantID, row.ShopID, row.ID)
	if expected == nil {
		query = query.Where("current_version_id IS NULL")
	} else {
		query = query.Where("current_version_id = ?", *expected)
	}
	result := query.Updates(map[string]any{"current_version_id": row.CurrentVersionID, "status": row.Status, "label": row.Label,
		"expires_at": row.ExpiresAt, "extra": row.Extra, "refresh_strategy": row.RefreshStrategy, "updated_at": time.Now().UTC(),
		"refresh_generation": row.RefreshGeneration, "retry_budget": row.RetryBudget, "consecutive_failures": row.ConsecutiveFailures,
		"refresh_at": row.RefreshAt, "last_refreshed_at": row.LastRefreshedAt})
	if result.Error != nil {
		return credentialDBError(result.Error)
	}
	if result.RowsAffected != 1 {
		return credentialVersionConflict()
	}
	return nil
}
