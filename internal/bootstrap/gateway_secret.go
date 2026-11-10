package bootstrap

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/db"
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
	defer s.state.invalidate()
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	return gatewayDBError(q.Table("api_client_secret").Create(&key).Error)
}

func (s *gatewayStore) Secrets(ctx context.Context, tenant, client int64) ([]gateway.Secret, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	key := "keys:" + strconv.FormatInt(tenant, 10) + ":" + strconv.FormatInt(client, 10)
	var states []struct {
		ID        int64
		Version   int
		Status    string
		ExpiresAt *time.Time
	}
	if !db.InTransaction(ctx) {
		if err := q.Table("api_client_secret").Select("id,version,status,expires_at").Where("tenant_id=? AND client_id=? AND status='active'", tenant, client).Order("id").Limit(3).Find(&states).Error; err != nil {
			return nil, gatewayDBError(err)
		}
		version, err := json.Marshal(states)
		if err != nil {
			return nil, gateway.Unavailable()
		}
		if cached, ok := s.state.get(key, string(version)); ok {
			return cloneGatewaySecrets(cached.([]gateway.Secret)), nil
		}
	}
	keys := []gateway.Secret{}
	err := q.Table("api_client_secret").Select(gatewayKeyColumns).
		Where("tenant_id=? AND client_id=? AND status='active'", tenant, client).
		Order("id").Limit(3).Find(&keys).Error
	if err == nil && !db.InTransaction(ctx) {
		states = nil
		for _, k := range keys {
			states = append(states, struct {
				ID        int64
				Version   int
				Status    string
				ExpiresAt *time.Time
			}{k.ID, k.Version, k.Status, k.ExpiresAt})
		}
		version, encodeErr := json.Marshal(states)
		if encodeErr != nil {
			return nil, gateway.Unavailable()
		}
		s.state.put(key, string(version), cloneGatewaySecrets(keys))
	}
	return keys, gatewayDBError(err)
}

func (s *gatewayStore) ExpireSecrets(ctx context.Context, tenant, client int64, now time.Time) error {
	defer s.state.invalidate()
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	return gatewayDBError(q.Table("api_client_secret").Where("tenant_id=? AND client_id=? AND status='active' AND expires_at<=?", tenant, client, now).Update("status", "expired").Error)
}

func (s *gatewayStore) SetSecretExpiry(ctx context.Context, tenant, client, id int64, expiry time.Time) error {
	defer s.state.invalidate()
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	result := q.Table("api_client_secret").Where("tenant_id=? AND client_id=? AND id=? AND status='active'", tenant, client, id).
		Update("expires_at", gorm.Expr("LEAST(expires_at, ?)", expiry))
	return gatewayMutationError(result)
}

func (s *gatewayStore) RevokeSecret(ctx context.Context, tenant, client, id int64) error {
	defer s.state.invalidate()
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	return gatewayMutationError(q.Table("api_client_secret").Where("tenant_id=? AND client_id=? AND id=?", tenant, client, id).Update("status", "revoked"))
}

func (s *gatewayStore) Touch(ctx context.Context, principal gateway.Principal, now time.Time) error {
	return s.database.Transaction(ctx, func(tx context.Context) error {
		q, cancel := s.database.WithContext(tx)
		defer cancel()
		var changed int
		err := q.Raw(`WITH current_client AS MATERIALIZED (
			SELECT c.id FROM api_client c JOIN tenant t ON t.id=c.tenant_id AND t.status='active'
			WHERE c.tenant_id=? AND c.id=? AND c.app_id=? AND c.owner_user_id=? AND c.status='active' FOR UPDATE OF c
		), used_key AS (
			UPDATE api_client_secret SET last_used_at=? WHERE tenant_id=? AND client_id IN (SELECT id FROM current_client)
			AND id=? AND status='active' AND (expires_at IS NULL OR expires_at>?) RETURNING client_id
		), used_client AS (
			UPDATE api_client SET last_used_at=? WHERE tenant_id=? AND id IN (SELECT client_id FROM used_key) RETURNING id
		) SELECT count(*) FROM used_client`, principal.TenantID, principal.ClientID, principal.AppID, principal.OwnerUserID,
			now, principal.TenantID, principal.SecretID, now, now, principal.TenantID).Row().Scan(&changed)
		if err != nil {
			return gatewayDBError(err)
		}
		if changed != 1 {
			s.state.invalidate()
			return gatewayAuthenticationError()
		}
		return nil
	})
}
