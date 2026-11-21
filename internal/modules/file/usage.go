package file

import (
	"context"
	"errors"
	"fmt"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
)

// Usage reports this user's metadata bytes across all of their providers.
type Usage struct {
	UsedBytes  int64 `json:"used_bytes"`
	QuotaBytes int64 `json:"quota_bytes"`
	FileCount  int64 `json:"file_count"`
}

// UsageStore supplies serialized per-user quota checks in the metadata transaction.
type UsageStore interface {
	FileUsage(context.Context, int64, int64) (int64, int64, error)
	LockFileUser(context.Context, int64, int64) error
}

// Usage returns occupancy against the current default provider's allowance.
func (s *Service) Usage(ctx context.Context, tenant, user int64) (Usage, error) {
	if tenant <= 0 || user <= 0 {
		return Usage{}, invalidFile()
	}
	bound, err := s.bound(ctx, nil, "")
	if err != nil {
		return Usage{}, err
	}
	return bound.usage(ctx, tenant, user)
}

func (s *Service) usage(ctx context.Context, tenant, user int64) (Usage, error) {
	store, ok := s.store.(UsageStore)
	if !ok {
		return Usage{}, errors.New("file usage persistence unavailable")
	}
	used, count, err := store.FileUsage(ctx, tenant, user)
	return Usage{UsedBytes: used, FileCount: count, QuotaBytes: s.quotaPerUserMB << 20}, err
}

func (s *Service) checkQuota(ctx context.Context, input Input, size int64) error {
	if s.quotaPerUserMB == 0 {
		return nil
	}
	usage, err := s.usage(ctx, input.TenantID, input.UserID)
	if err != nil {
		return err
	}
	if size > usage.QuotaBytes-usage.UsedBytes {
		return errs.New(errs.ErrInvalidParam, 413, fmt.Sprintf("存储空间不足：已用 %d MB / 上限 %d MB", usage.UsedBytes>>20, s.quotaPerUserMB))
	}
	return nil
}
