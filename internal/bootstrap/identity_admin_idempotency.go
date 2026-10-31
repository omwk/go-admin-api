package bootstrap

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
	"gorm.io/gorm"
)

func (s *adminStore) Replay(ctx context.Context, tenant int64, scope, key, hash string) (repo.SavedResponse, bool, error) {
	if err := requireAdminTenant(ctx, tenant, "Admin.Replay"); err != nil {
		return repo.SavedResponse{}, false, err
	}
	q, cancel := s.raw(ctx)
	defer cancel()
	var row struct {
		RequestHash  string
		ResponseCode int
		ResponseBody json.RawMessage
	}
	if tenant <= 0 {
		return repo.SavedResponse{}, false, errors.New("idempotency tenant is required")
	}
	err := q.Table("idempotency_key").Select("request_hash, response_code, response_body").
		Where("tenant_id = ? AND scope = ? AND key = ? AND expires_at > now() AND status = ?", tenant, scope, key, "completed").Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return repo.SavedResponse{}, false, nil
	}
	if err != nil {
		return repo.SavedResponse{}, false, err
	}
	if row.RequestHash != hash {
		return repo.SavedResponse{}, false, errs.New(errs.ErrIdempotencyMismatch, 409, "幂等键请求内容不一致")
	}
	return repo.SavedResponse{Status: row.ResponseCode, Data: row.ResponseBody}, true, nil
}

func (s *adminStore) SaveResponse(ctx context.Context, tenant int64, scope, key, hash string, result repo.SavedResponse) error {
	if err := requireAdminTenant(ctx, tenant, "Admin.SaveResponse"); err != nil {
		return err
	}
	q, cancel := s.raw(ctx)
	defer cancel()
	if tenant <= 0 {
		return errors.New("idempotency tenant is required")
	}
	data := result.Data
	if len(data) == 0 {
		data = json.RawMessage("null")
	}
	return q.Exec(`INSERT INTO idempotency_key (tenant_id, scope, key, request_hash, response_code, response_body, status)
		VALUES (?, ?, ?, ?, ?, ?::jsonb, 'completed')
		ON CONFLICT (tenant_id, scope, key) DO UPDATE SET request_hash=EXCLUDED.request_hash,
		response_code=EXCLUDED.response_code, response_body=EXCLUDED.response_body, status='completed', expires_at=now()+interval '24 hours'`,
		tenant, scope, key, hash, result.Status, string(data)).Error
}
