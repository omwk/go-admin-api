package catalog

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/idem"
	"github.com/omwk/go-admin-api/internal/modules/catalog/port"
)

// ExportService owns frozen CSV generation and private download authorization.
type ExportService struct {
	store     ExportStore
	objects   port.ObjectStore
	authorize ProductAuthorizer
	now       func() time.Time
}

// NewExportService constructs the export service without performing IO.
func NewExportService(store ExportStore, objects port.ObjectStore, authorize ProductAuthorizer) (*ExportService, error) {
	if store == nil || objects == nil || authorize == nil {
		return nil, errors.New("export dependencies required")
	}
	return &ExportService{store: store, objects: objects, authorize: authorize, now: time.Now}, nil
}

// Create snapshots filters, sort, tenant, owner and field version before dispatch.
func (s *ExportService) Create(ctx context.Context, tenant, user int64, input ExportRequest, operation *idem.Operation) (ExportJob, error) {
	ctx, err := s.authorize(ctx, tenant, user)
	if err != nil {
		return ExportJob{}, err
	}
	query, err := normalizeProductQuery(ProductListQuery{ProductFilter: input.Filters, Sort: input.Sort})
	if err != nil || input.Filters.ShopID < 0 || input.Filters.PlatformID < 0 || input.Filters.CategoryID < 0 ||
		(input.Filters.Status != "" && !validProductStatus(input.Filters.Status)) ||
		(input.Filters.PriceMinCents != nil && *input.Filters.PriceMinCents < 0) ||
		(input.Filters.PriceMaxCents != nil && *input.Filters.PriceMaxCents < 0) {
		return ExportJob{}, invalidProduct()
	}
	snapshot := ExportSnapshot{ExportRequest: ExportRequest{Filters: query.ProductFilter, Sort: query.Sort}, FieldsVersion: 1}
	return s.store.CreateExport(ctx, tenant, user, snapshot, operation)
}

// Status signs only the object referenced by a job owned by this tenant and user.
func (s *ExportService) Status(ctx context.Context, tenant, user, id int64) (ExportStatus, error) {
	ctx, err := s.authorize(ctx, tenant, user)
	if err != nil {
		return ExportStatus{}, err
	}
	job, err := s.store.ReadExport(ctx, tenant, user, id)
	if err != nil {
		return ExportStatus{}, err
	}
	result := ExportStatus{ExportJob: job}
	if job.Status != "succeeded" {
		return result, nil
	}
	now := s.now().UTC()
	if job.ExpiresAt == nil || job.ExpiresAt.Sub(now) < time.Second {
		result.Status = "expired"
		return result, nil
	}
	ttl := min(time.Hour, job.ExpiresAt.Sub(now)).Truncate(time.Second)
	signed, err := s.objects.PresignGet(ctx, job.ObjectKey, ttl)
	if err != nil {
		return ExportStatus{}, err
	}
	expires := now.Add(ttl)
	result.DownloadURL, result.DownloadExpiresAt = signed, &expires
	return result, nil
}

// Execute starts a fenced attempt and streams its frozen rows to storage.
func (s *ExportService) Execute(ctx context.Context, tenant, id int64) error {
	ctx, cancel := context.WithTimeout(ctx, ExportBudget)
	defer cancel()
	job, err := s.store.BeginExport(ctx, tenant, id)
	if err != nil || job.Status == "succeeded" || job.Status == "expired" {
		return err
	}
	return s.Upload(ctx, job)
}

// ExportObjectKey constructs a private immutable key from trusted database identity.
func ExportObjectKey(job ExportJob) string {
	return fmt.Sprintf("exports/%d/%d/attempt-%d.csv", job.TenantID, job.ID, job.Attempt)
}

// Upload completes one claimed attempt; CAS loss cannot publish its object.
func (s *ExportService) Upload(ctx context.Context, job ExportJob) (retErr error) {
	if job.TenantID <= 0 || job.ID <= 0 || job.Attempt <= 0 || job.Status != "running" {
		return errors.New("invalid export attempt")
	}
	ctx, cancel := context.WithTimeout(ctx, ExportBudget)
	defer cancel()
	key := ExportObjectKey(job)
	defer func() {
		if retErr == nil {
			return
		}
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer stop()
		retErr = errors.Join(retErr, s.store.FailExport(cleanup, job), s.CleanupObject(cleanup, job.TenantID, key))
	}()
	digest, err := s.uploadCSV(ctx, job, key)
	if err != nil {
		return err
	}
	if err := s.verifyObject(ctx, key, digest); err != nil {
		return err
	}
	published, err := s.store.PublishExport(ctx, job, key, digest)
	if err != nil {
		return err
	}
	if !published {
		return s.CleanupObject(ctx, job.TenantID, key)
	}
	return nil
}

func (s *ExportService) uploadCSV(ctx context.Context, job ExportJob, key string) (ExportDigest, error) {
	reader, writer := io.Pipe()
	type result struct {
		digest ExportDigest
		err    error
	}
	done := make(chan result, 1)
	go func() {
		digest, err := s.writeCSV(ctx, job, writer)
		closeErr := writer.CloseWithError(err)
		done <- result{digest, errors.Join(err, closeErr)}
	}()
	size, uploadErr := s.objects.Put(ctx, key, reader, -1)
	closeErr := reader.CloseWithError(uploadErr)
	written := <-done
	if err := errors.Join(uploadErr, closeErr, written.err); err != nil {
		return ExportDigest{}, err
	}
	if size != written.digest.Size {
		return ExportDigest{}, errors.New("export uploaded size mismatch")
	}
	return written.digest, nil
}

// ExportNotFound hides job existence across both tenant and owner boundaries.
func ExportNotFound() error { return errs.New(errs.ErrNotFound, 404, "导出任务不存在") }
