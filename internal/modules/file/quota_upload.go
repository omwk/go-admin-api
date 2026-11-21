package file

import (
	"context"
	"encoding/hex"
	"io"
	"strings"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/idem"
	"github.com/omwk/go-admin-api/internal/modules/file/model"
)

// A full allowance must still allow an exact idempotent replay, without object writes.
func (s *Service) quotaPreflight(ctx context.Context, input Input, name, source string, quotaErr error) (View, idem.Result, error) {
	if strings.TrimSpace(input.IdempotencyKey) == "" || errs.From(quotaErr).HTTPStatus != 413 {
		return View{}, idem.Result{}, quotaErr
	}
	prefix, size, mime, err := streamUpload(input, func(_ string, reader io.Reader) (bool, error) {
		_, err := io.Copy(io.Discard, reader)
		return err == nil, err
	})
	if err != nil {
		return View{}, idem.Result{}, err
	}
	if err := validateFile(name, input.DeclaredMIME, mime, size, prefix.data); err != nil {
		return View{}, idem.Result{}, err
	}
	row := model.FileObject{OriginalName: name, Purpose: source, MIME: mime, SizeBytes: size, SHA256: hex.EncodeToString(prefix.digest[:])}
	return s.quotaReplay(ctx, input, row, quotaErr)
}

func (s *Service) quotaReplay(ctx context.Context, input Input, row model.FileObject, quotaErr error) (View, idem.Result, error) {
	if strings.TrimSpace(input.IdempotencyKey) == "" || s.idem == nil || errs.From(quotaErr).HTTPStatus != 413 {
		return View{}, idem.Result{}, quotaErr
	}
	op, err := s.operation(input, row.OriginalName, row.Purpose, row.MIME, row.SizeBytes, row.SHA256)
	if err != nil {
		return View{}, idem.Result{}, err
	}
	var result idem.Result
	err = s.store.FileTransaction(ctx, func(tx context.Context) error {
		claim, err := s.idem.Claim(tx, *op)
		if err != nil {
			return mapFileIdempotencyError(err)
		}
		if claim.Action != idem.Replay {
			return quotaErr // Roll back the new claim together with the rejected upload.
		}
		result = claim.Result
		return nil
	})
	return View{}, result, err
}
