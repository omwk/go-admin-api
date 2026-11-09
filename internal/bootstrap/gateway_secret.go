package bootstrap

import (
	"context"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/gateway"
	"gorm.io/gorm"
)

const gatewayKeyColumns = "id,tenant_id,client_id,fingerprint,secret_prefix,cipher_text,nonce,key_version,version,status,expires_at,created_at,last_used_at"

func (s *gatewayStore) NextSecretID(ctx context.Context) (int64, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var id int64
	err := q.Raw("SELECT nextval('api_client_secret_id_seq')").Scan(&id).Error
	return id, gatewayDBError(err)
}

func (s *gatewayStore) InsertSecret(ctx context.Context, key gateway.Secret) error {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	return gatewayDBError(q.Table("api_client_secret").Create(&key).Error)
}

func (s *gatewayStore) Secrets(ctx context.Context, tenant, client int64) ([]gateway.Secret, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	keys := []gateway.Secret{}
	err := q.Table("api_client_secret").Select(gatewayKeyColumns).
		Where("tenant_id=? AND client_id=? AND status='active'", tenant, client).
		Order("id").Limit(3).Find(&keys).Error
	return keys, gatewayDBError(err)
}

func (s *gatewayStore) ExpireSecrets(ctx context.Context, tenant, client int64, now time.Time) error {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	return gatewayDBError(q.Table("api_client_secret").Where("tenant_id=? AND client_id=? AND status='active' AND expires_at<=?", tenant, client, now).Update("status", "expired").Error)
}

func (s *gatewayStore) SetSecretExpiry(ctx context.Context, tenant, client, id int64, expiry time.Time) error {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	result := q.Table("api_client_secret").Where("tenant_id=? AND client_id=? AND id=? AND status='active'", tenant, client, id).
		Update("expires_at", gorm.Expr("LEAST(expires_at, ?)", expiry))
	return gatewayMutationError(result)
}

func (s *gatewayStore) RevokeSecret(ctx context.Context, tenant, client, id int64) error {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	return gatewayMutationError(q.Table("api_client_secret").Where("tenant_id=? AND client_id=? AND id=?", tenant, client, id).Update("status", "revoked"))
}

func (s *gatewayStore) Touch(ctx context.Context, principal gateway.Principal, now time.Time) error {
	return s.database.Transaction(ctx, func(tx context.Context) error {
		client, err := s.LockClient(tx, principal.TenantID, principal.ClientID)
		if err != nil {
			return err
		}
		if client.Status != "active" || client.AppID != principal.AppID || client.OwnerUserID != principal.OwnerUserID {
			return gateway.PublicError(gatewayAuthenticationError())
		}
		q, cancel := s.database.WithContext(tx)
		defer cancel()
		result := q.Table("api_client_secret").
			Where("tenant_id=? AND client_id=? AND id=? AND status='active' AND (expires_at IS NULL OR expires_at>?)", principal.TenantID, principal.ClientID, principal.SecretID, now).
			Update("last_used_at", now)
		if result.Error != nil {
			return gatewayDBError(result.Error)
		}
		if result.RowsAffected != 1 {
			return gatewayAuthenticationError()
		}
		return gatewayMutationError(q.Table("api_client").Where("tenant_id=? AND id=?", principal.TenantID, principal.ClientID).Update("last_used_at", now))
	})
}
