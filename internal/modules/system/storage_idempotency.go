package system

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/idem"
)

type storageResponseEnvelope struct {
	Cipher, Nonce []byte
	Version       int
}

// Execute encrypts response snapshots so idempotency never persists plaintext secrets.
func (s *StorageService) Execute(ctx context.Context, op *idem.Operation, run func(context.Context) (idem.Result, error)) (result idem.Result, err error) {
	if op == nil {
		return run(ctx)
	}
	if s.idempotency == nil || s.sealer == nil {
		return result, errors.New("storage idempotency unavailable")
	}
	err = s.store.Transaction(ctx, func(tx context.Context) error {
		claim, err := s.idempotency.Claim(tx, *op)
		if err != nil {
			return err
		}
		aad := "storage-response:v1:" + op.RequestHash
		if claim.Action == idem.Replay {
			var envelope storageResponseEnvelope
			if json.Unmarshal(claim.Result.Body, &envelope) != nil {
				return errors.New("invalid encrypted storage response")
			}
			body, err := s.sealer.Open(envelope.Cipher, envelope.Nonce, aad, envelope.Version)
			if err != nil {
				return errors.New("storage response decryption failed")
			}
			result = idem.Result{Status: claim.Result.Status, Body: body}
			return nil
		}
		result, err = run(tx)
		if err != nil {
			return err
		}
		cipher, nonce, version, err := s.sealer.Seal(result.Body, aad)
		if err != nil {
			return errors.New("storage response encryption failed")
		}
		body, err := json.Marshal(storageResponseEnvelope{cipher, nonce, version})
		if err != nil {
			return err
		}
		_, err = s.idempotency.Complete(tx, *op, idem.Result{Status: result.Status, Body: body})
		return err
	})
	if errors.Is(err, idem.ErrMismatch) {
		err = errs.New(errs.ErrIdempotencyMismatch, 409, "幂等请求不一致")
	}
	if errors.Is(err, idem.ErrProcessing) {
		err = errs.New(errs.ErrConflict, 409, "请求处理中")
	}
	return result, err
}
