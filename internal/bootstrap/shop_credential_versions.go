package bootstrap

import (
	"context"

	"github.com/omwk/go-admin-api/internal/modules/shop/repo"
)

func (s *shopStore) CredentialVersions(ctx context.Context, tenant, id int64) ([]repo.CredentialVersionView, error) {
	if _, err := s.credentialParent(ctx, tenant, id, "CredentialVersions"); err != nil {
		return nil, err
	}
	q := s.credentialQuery(ctx, tenant, "CredentialVersions")
	defer q.Close()
	rows := []repo.CredentialVersionView{}
	err := q.Joins(`JOIN credential_version history ON history.tenant_id = shop_credential.tenant_id
		AND history.credential_id = shop_credential.id AND history.id <> shop_credential.current_version_id`).
		Select("history.id, history.version_no, history.fingerprint, history.issued_at, history.expires_at, history.created_reason, history.created_by, history.retired_at, history.purge_after").
		Where("shop_credential.id = ? AND history.retired_at IS NOT NULL", id).
		Order("history.version_no DESC, history.id DESC").Find(&rows)
	return rows, credentialDBError(err)
}
