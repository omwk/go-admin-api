package bootstrap

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/kernel/idem"
	"gorm.io/gorm"
)

type idempotencyStore struct {
	database *db.DB
}

var _ idem.Store = (*idempotencyStore)(nil)

func newIdempotencyStore(database *db.DB) *idempotencyStore {
	return &idempotencyStore{database: database}
}

func (s *idempotencyStore) Claim(ctx context.Context, operation idem.Operation) (idem.Claim, error) {
	if operation.TenantID <= 0 || operation.UserID <= 0 || operation.Scope == "" || operation.KeyDigest == "" || operation.RequestHash == "" {
		return idem.Claim{}, errors.New("idempotency operation is invalid")
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var acquired bool
	if err := q.Raw("SELECT pg_try_advisory_xact_lock(hashtextextended(?, 0))", lockName(operation)).Scan(&acquired).Error; err != nil {
		return idem.Claim{}, fmt.Errorf("claim idempotency lock: %w", err)
	}
	if !acquired {
		return idem.Claim{}, idem.ErrProcessing
	}
	if err := q.Exec(`DELETE FROM idempotency_key WHERE tenant_id = ? AND scope = ? AND key = ? AND expires_at <= now()`, operation.TenantID, operation.Scope, operation.KeyDigest).Error; err != nil {
		return idem.Claim{}, fmt.Errorf("remove expired idempotency key: %w", err)
	}
	var row struct {
		RequestHash  string
		ResponseCode sql.NullInt64
		ResponseBody json.RawMessage
		Status       string
	}
	err := q.Table("idempotency_key").Select("request_hash, response_code, response_body, status").
		Where("tenant_id = ? AND scope = ? AND key = ? AND expires_at > now()", operation.TenantID, operation.Scope, operation.KeyDigest).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		if err := q.Exec(`INSERT INTO idempotency_key
			(tenant_id, scope, key, request_hash, response_code, response_body, status, expires_at)
			VALUES (?, ?, ?, ?, NULL, 'null'::jsonb, 'processing', now() + interval '24 hours')`,
			operation.TenantID, operation.Scope, operation.KeyDigest, operation.RequestHash).Error; err != nil {
			return idem.Claim{}, fmt.Errorf("insert idempotency placeholder: %w", err)
		}
		return idem.Claim{Action: idem.Execute}, nil
	}
	if err != nil {
		return idem.Claim{}, fmt.Errorf("load idempotency result: %w", err)
	}
	if row.RequestHash != operation.RequestHash {
		return idem.Claim{}, idem.ErrMismatch
	}
	if row.Status != "completed" || !row.ResponseCode.Valid || row.ResponseCode.Int64 < 200 || row.ResponseCode.Int64 >= 300 {
		return idem.Claim{}, idem.ErrProcessing
	}
	result, err := idem.ResultJSON(int(row.ResponseCode.Int64), row.ResponseBody)
	if err != nil {
		return idem.Claim{}, fmt.Errorf("decode idempotency result: %w", err)
	}
	return idem.Claim{Action: idem.Replay, Result: result}, nil
}

func (s *idempotencyStore) Complete(ctx context.Context, operation idem.Operation, result idem.Result) (idem.Result, error) {
	if result.Status < 200 || result.Status >= 300 {
		return idem.Result{}, errors.New("only successful idempotency results can be completed")
	}
	body := result.Body
	if len(body) == 0 {
		body = []byte("null")
	}
	if err := qExec(ctx, s.database, `UPDATE idempotency_key SET response_code = ?, response_body = ?::jsonb, status = 'completed'
		WHERE tenant_id = ? AND scope = ? AND key = ? AND request_hash = ? AND status = 'processing' AND expires_at > now()`,
		result.Status, string(body), operation.TenantID, operation.Scope, operation.KeyDigest, operation.RequestHash); err != nil {
		return idem.Result{}, fmt.Errorf("complete idempotency result: %w", err)
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var row struct {
		ResponseCode int
		ResponseBody json.RawMessage
	}
	if err := q.Table("idempotency_key").Select("response_code, response_body").
		Where("tenant_id = ? AND scope = ? AND key = ? AND status = 'completed'", operation.TenantID, operation.Scope, operation.KeyDigest).Take(&row).Error; err != nil {
		return idem.Result{}, fmt.Errorf("read completed idempotency result: %w", err)
	}
	canonical, err := idem.ResultJSON(row.ResponseCode, row.ResponseBody)
	if err != nil {
		return idem.Result{}, fmt.Errorf("decode completed idempotency result: %w", err)
	}
	return canonical, nil
}

func qExec(ctx context.Context, database *db.DB, query string, args ...any) error {
	q, cancel := database.WithContext(ctx)
	defer cancel()
	result := q.Exec(query, args...)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("idempotency placeholder is missing")
	}
	return nil
}

func lockName(operation idem.Operation) string {
	return fmt.Sprintf("idempotency:%d:%d:%s:%s", operation.TenantID, operation.UserID, operation.Scope, operation.KeyDigest)
}
