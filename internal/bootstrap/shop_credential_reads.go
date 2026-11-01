package bootstrap

import (
	"context"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/shop/model"
	"github.com/omwk/go-admin-api/internal/modules/shop/repo"
)

func (s *shopStore) Credentials(ctx context.Context, tenant, shopID int64, page repo.Page, audit model.CredentialAudit) ([]repo.CredentialRow, int64, error) {
	root, cancel := s.query(ctx, tenant, &model.Shop{}, "Credential.Credentials")
	defer cancel()
	var parent model.Shop
	if err := root.Select("shop.id").Where("shop.id = ?", shopID).Take(&parent); err != nil {
		return nil, 0, credentialDBError(err)
	}
	q := s.credentialQuery(ctx, tenant, "Credentials")
	defer q.Close()
	q = q.Where("shop.id = ?", shopID)
	var total int64
	if err := q.Fork().Count(&total); err != nil {
		return nil, 0, credentialDBError(err)
	}
	rows := []repo.CredentialRow{}
	err := q.Select(publicMetadataColumns).Order("shop_credential.created_at desc, shop_credential.id desc").Limit(page.Size).Offset((page.Number - 1) * page.Size).Find(&rows)
	if err != nil {
		return nil, 0, credentialDBError(err)
	}
	for _, row := range rows {
		if err := s.appendCredentialAudit(ctx, audit, row); err != nil {
			return nil, 0, err
		}
	}
	return rows, total, nil
}

func (s *shopStore) Credential(ctx context.Context, tenant, id int64, audit model.CredentialAudit) (repo.CredentialRow, error) {
	row, err := s.credentialParent(ctx, tenant, id, "Credential")
	if err != nil {
		return repo.CredentialRow{}, err
	}
	return row, s.appendCredentialAudit(ctx, audit, row)
}

func (s *shopStore) CheckCredential(ctx context.Context, tenant, id int64, audit model.CredentialAudit) error {
	row, err := s.lockedCredential(ctx, tenant, id, "CheckCredential")
	if err != nil {
		return err
	}
	return s.appendCredentialAudit(ctx, audit, row)
}

func (s *shopStore) CredentialAudits(ctx context.Context, tenant, id int64, page repo.Page, audit model.CredentialAudit) ([]model.CredentialAudit, int64, error) {
	row, err := s.credentialParent(ctx, tenant, id, "CredentialAudits")
	if err != nil {
		return nil, 0, err
	}
	q := s.credentialQuery(ctx, tenant, "CredentialAudits")
	defer q.Close()
	q = q.Where("shop_credential.id = ?", id).Joins("JOIN credential_audit ca ON ca.tenant_id = shop_credential.tenant_id AND ca.credential_id = shop_credential.id")
	var total int64
	if err := q.Fork().Count(&total); err != nil {
		return nil, 0, credentialDBError(err)
	}
	rows := []model.CredentialAudit{}
	err = q.Select("ca.id, ca.action, ca.result, ca.error_code, ca.detail, ca.created_at").
		Order("ca.created_at desc, ca.id desc").Limit(page.Size).Offset((page.Number - 1) * page.Size).Find(&rows)
	if err != nil {
		return nil, 0, credentialDBError(err)
	}
	if err := s.appendCredentialAudit(ctx, audit, row); err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

func credentialVersionConflict() error {
	return errs.New(errs.ErrVersionConflict, 409, "凭据版本冲突")
}
