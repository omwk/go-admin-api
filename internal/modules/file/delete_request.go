package file

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/omwk/go-admin-api/internal/kernel/idem"
)

// DeleteRequest replays successful deletions without reopening removed metadata.
func (s *Service) DeleteRequest(ctx context.Context, tenant, user, id int64, key string) error {
	if tenant <= 0 || user <= 0 || id <= 0 {
		return invalidFile()
	}
	if strings.TrimSpace(key) == "" {
		return s.Delete(ctx, tenant, user, id)
	}
	op, err := idem.Prepare(tenant, user, idem.Input{Key: key, Method: "DELETE", Route: "/api/v1/admin/files/:id", PathParams: []string{strconv.FormatInt(id, 10)}})
	if err != nil {
		return invalidFile()
	}
	return s.store.FileTransaction(ctx, func(tx context.Context) error {
		if s.idem == nil {
			return errors.New("file idempotency unavailable")
		}
		claim, err := s.idem.Claim(tx, op)
		if err != nil {
			return mapFileIdempotencyError(err)
		}
		if claim.Action == idem.Replay {
			return nil
		}
		if err := s.Delete(tx, tenant, user, id); err != nil {
			return err
		}
		_, err = s.idem.Complete(tx, op, idem.Result{Status: 204, Body: []byte("null")})
		return err
	})
}
