package file

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/omwk/go-admin-api/internal/kernel/idem"
	"github.com/omwk/go-admin-api/internal/modules/file/model"
	"github.com/omwk/go-admin-api/internal/modules/file/repo"
)

func (s *Service) operation(input Input, name, purpose, sniffed string, size int64, sha string) (*idem.Operation, error) {
	if strings.TrimSpace(input.IdempotencyKey) == "" {
		return nil, nil
	}
	body, err := json.Marshal(map[string]any{"declared_mime": input.DeclaredMIME, "filename": name, "mime": sniffed, "source": purpose, "sha256": sha, "size_bytes": size})
	if err != nil {
		return nil, err
	}
	route := "/api/v1/admin/files"
	if purpose == "avatar" {
		route = "/api/v1/auth/profile/avatar"
	}
	op, err := idem.Prepare(input.TenantID, input.UserID, idem.Input{Key: input.IdempotencyKey, Method: http.MethodPost, Route: route, Body: body})
	if err != nil {
		return nil, mapFileIdempotencyError(err)
	}
	return &op, nil
}

func (s *Service) register(ctx context.Context, input Input, operation *idem.Operation, row model.FileObject) (View, idem.Result, bool, error) {
	var view View
	var result idem.Result
	var keepObject bool
	err := s.store.FileTransaction(ctx, func(tx context.Context) error {
		if operation != nil && s.idem != nil {
			claim, err := s.idem.Claim(tx, *operation)
			if err != nil {
				return mapFileIdempotencyError(err)
			}
			if claim.Action == idem.Replay {
				result = claim.Result
				existing, findErr := s.store.FindFileByHash(tx, row.TenantID, row.SHA256)
				keepObject = findErr == nil && existing.ObjectKey == row.ObjectKey
				return nil
			}
		}
		var err error
		view, keepObject, err = s.persistUpload(tx, input, row)
		if err != nil {
			return err
		}
		if operation != nil && s.idem != nil {
			var err error
			if input.Serialize != nil {
				result, err = input.Serialize(http.StatusCreated, view)
			} else {
				result, err = idem.JSONResult(http.StatusCreated, view)
			}
			if err != nil {
				return err
			}
			result, err = s.idem.Complete(tx, *operation, result)
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return View{}, idem.Result{}, false, err
	}
	return view, result, keepObject, nil
}

func (s *Service) persistUpload(ctx context.Context, input Input, row model.FileObject) (View, bool, error) {
	if locker, ok := s.store.(UsageStore); ok {
		if err := locker.LockFileUser(ctx, input.TenantID, input.UserID); err != nil {
			return View{}, false, err
		}
	}
	if locker, ok := s.store.(repo.FileHashLocker); ok {
		if err := locker.LockFileHash(ctx, row.TenantID, row.SHA256); err != nil {
			return View{}, false, err
		}
	}
	if row.Purpose != "avatar" {
		existing, err := s.store.FindFileByHash(ctx, row.TenantID, row.SHA256)
		if err == nil {
			view, err := s.project(ctx, existing)
			return view, existing.ObjectKey == row.ObjectKey, err
		}
		if !errors.Is(err, ErrFileNotFound) {
			return View{}, false, err
		}
	}
	if err := s.checkQuota(ctx, input, row.SizeBytes); err != nil {
		return View{}, false, err
	}
	if err := s.store.CreateFile(ctx, &row); err != nil {
		return View{}, false, err
	}
	if input.OnRegister != nil {
		if err := input.OnRegister(ctx); err != nil {
			return View{}, false, err
		}
	}
	if s.record != nil {
		if err := s.recordUpload(ctx, input, row); err != nil {
			return View{}, false, err
		}
	}
	view, err := s.project(ctx, row)
	return view, true, err
}
