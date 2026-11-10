package gateway

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
)

func (s *CallService) existingRequest(ctx context.Context, wanted Call) (Call, error) {
	current, err := s.store.Call(ctx, wanted.ID)
	if err != nil && wanted.IdempotencyKey != "" {
		current, err = s.store.RequestCall(ctx, wanted)
	}
	if err != nil {
		return Call{}, err
	}
	if wanted.IdempotencyKey != "" {
		if current.TenantID != wanted.TenantID || current.ClientID != wanted.ClientID || current.IdempotencyKey != wanted.IdempotencyKey || current.RequestHash != wanted.RequestHash {
			return Call{}, errs.New(10006, 409, "幂等键与请求不一致")
		}
	} else if !sameCall(current, wanted) {
		return Call{}, Unavailable()
	}
	return current, nil
}

func replayCall(c Call) (json.RawMessage, error) {
	if c.State == "processing" {
		return nil, errs.New(10004, 409, "请求正在处理")
	}
	if c.State == "succeeded" {
		return c.Result, nil
	}
	code, err := strconv.Atoi(c.ErrorCode)
	if err != nil {
		return nil, Unavailable()
	}
	return nil, PublicError(errs.New(code, c.StatusCode, ""))
}
