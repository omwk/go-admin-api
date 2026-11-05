package catalog

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/catalog/port"
)

// ParseExportObjectKey accepts server-generated attempt or temporary export keys only.
func ParseExportObjectKey(key string) (tenant, id int64, attempt int, valid bool) {
	parts := strings.Split(key, "/")
	if len(parts) != 4 || parts[0] != "exports" {
		return
	}
	tenant, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || tenant <= 0 {
		return 0, 0, 0, false
	}
	id, err = strconv.ParseInt(parts[2], 10, 64)
	if err != nil || id <= 0 {
		return 0, 0, 0, false
	}
	name := parts[3]
	if strings.HasPrefix(name, "tmp-") && len(name) > 4 {
		return tenant, id, 0, true
	}
	if !strings.HasPrefix(name, "attempt-") || !strings.HasSuffix(name, ".csv") {
		return 0, 0, 0, false
	}
	attempt, err = strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(name, "attempt-"), ".csv"))
	if err != nil || attempt <= 0 || key != ExportObjectKey(ExportJob{TenantID: tenant, ID: id, Attempt: attempt}) {
		return 0, 0, 0, false
	}
	return tenant, id, attempt, true
}

// CleanupObject rechecks the database pointer before any physical deletion.
func (s *ExportService) CleanupObject(ctx context.Context, tenant int64, key string) error {
	return s.cleanupObject(ctx, tenant, key, true)
}

// RetryCleanup reuses the existing outbox obligation instead of emitting another.
func (s *ExportService) RetryCleanup(ctx context.Context, tenant, id int64, key string) error {
	if key != "" {
		return s.cleanupObject(ctx, tenant, key, false)
	}
	return s.cleanup(ctx, tenant, id, false)
}

func (s *ExportService) cleanupObject(ctx context.Context, tenant int64, key string, persistFailure bool) error {
	keyTenant, _, _, valid := ParseExportObjectKey(key)
	if !valid || keyTenant != tenant {
		return errors.New("invalid export cleanup identity")
	}
	allowed, err := s.store.CanDeleteExportObject(ctx, key)
	if err == nil && !allowed {
		return nil
	}
	if err == nil {
		err = s.objects.Delete(ctx, key)
	}
	if err != nil && persistFailure {
		return errors.Join(err, s.store.QueueExportCleanup(ctx, tenant, key))
	}
	return err
}

// Cleanup scans a prefix; orphan/temp objects require one hour of age.
func (s *ExportService) Cleanup(ctx context.Context, tenant, id int64) error {
	return s.cleanup(ctx, tenant, id, true)
}

func (s *ExportService) cleanup(ctx context.Context, tenant, id int64, persistFailure bool) error {
	if err := s.store.ExpireExports(ctx, tenant, id, s.now().UTC()); err != nil {
		return err
	}
	scanner, ok := s.objects.(port.ObjectScanner)
	if !ok {
		return errors.New("export storage scanning required")
	}
	prefix := "exports/"
	if tenant > 0 && id > 0 {
		prefix = fmt.Sprintf("exports/%d/%d/", tenant, id)
	}
	var failures error
	err := scanner.Scan(ctx, prefix, func(info port.ObjectInfo) error {
		keyTenant, _, attempt, valid := ParseExportObjectKey(info.Key)
		if !valid || !strings.HasPrefix(info.Key, prefix) || info.ModifiedAt.IsZero() {
			return nil
		}
		if attempt == 0 || tenant == 0 {
			if s.now().UTC().Sub(info.ModifiedAt) <= time.Hour {
				return nil
			}
		}
		failures = errors.Join(failures, s.cleanupObject(ctx, keyTenant, info.Key, persistFailure))
		return nil
	})
	return errors.Join(err, failures)
}
