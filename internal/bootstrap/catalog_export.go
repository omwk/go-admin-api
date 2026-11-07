package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/idem"
	"github.com/omwk/go-admin-api/internal/kernel/outbox"
	"github.com/omwk/go-admin-api/internal/modules/catalog"
	"gorm.io/gorm"
)

type exportStore struct {
	database *db.DB
	bucket   string
}

const exportColumns = "id, tenant_id, kind, status, filters, requested_by, file_id, attempt, row_total, row_count, error_msg, object_key, expires_at, created_at, started_at, finished_at"

// NewExportStore constructs the explicit tenant/owner-scoped persistence adapter.
func NewExportStore(database *db.DB, bucket string) catalog.ExportStore {
	return &exportStore{database: database, bucket: bucket}
}

func (s *exportStore) ReadExport(ctx context.Context, tenant, user, id int64) (catalog.ExportJob, error) {
	if err := catalogQuery("Export.Read"); err != nil {
		return catalog.ExportJob{}, err
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var job catalog.ExportJob
	err := q.Select(exportColumns).Where("tenant_id=? AND requested_by=? AND id=?", tenant, user, id).Take(&job).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return job, catalog.ExportNotFound()
	}
	return job, err
}

func (s *exportStore) CreateExport(ctx context.Context, tenant, user int64, snapshot catalog.ExportSnapshot, op *idem.Operation) (job catalog.ExportJob, err error) {
	if err := catalogQuery("Export.Create"); err != nil {
		return job, err
	}
	if op != nil && (op.TenantID != tenant || op.UserID != user) {
		return job, errs.New(errs.ErrPermissionDenied, 403, "无权限")
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return job, err
	}
	return s.createExportJob(ctx, tenant, user, "products", raw, op)
}

func (s *exportStore) createExportJob(ctx context.Context, tenant, user int64, kind string, raw json.RawMessage, op *idem.Operation) (job catalog.ExportJob, err error) {
	if op != nil && (op.TenantID != tenant || op.UserID != user) {
		return job, errs.New(errs.ErrPermissionDenied, 403, "无权限")
	}
	err = s.database.Transaction(ctx, func(ctx context.Context) error {
		idempotency := newIdempotencyStore(s.database)
		if op != nil {
			claim, err := idempotency.Claim(ctx, *op)
			if err != nil {
				return err
			}
			if claim.Action == idem.Replay {
				return json.Unmarshal(claim.Result.Body, &job)
			}
		}
		q, cancel := s.database.WithContext(ctx)
		defer cancel()
		if err := lockExportAdmission(q); err != nil {
			return err
		}
		if err := checkExportAdmission(q, tenant, user, 0, true); err != nil {
			return err
		}
		job = catalog.ExportJob{TenantID: tenant, RequestedBy: user, Kind: kind, Status: "running", Filters: raw}
		if err := q.Create(&job).Error; err != nil {
			return err
		}
		if err := s.emitExport(ctx, tenant, job.ID, "export.dispatch", ""); err != nil {
			return err
		}
		if op == nil {
			return nil
		}
		result, err := idem.JSONResult(201, job)
		if err != nil {
			return err
		}
		_, err = idempotency.Complete(ctx, *op, result)
		return err
	})
	if errors.Is(err, idem.ErrMismatch) {
		err = errs.New(errs.ErrIdempotencyMismatch, 409, "幂等请求不一致")
	}
	if errors.Is(err, idem.ErrProcessing) {
		err = errs.New(errs.ErrConflict, 409, "请求处理中")
	}
	return job, err
}

func (s *exportStore) emitExport(ctx context.Context, tenant, id int64, eventType, key string) error {
	var duration *float64
	if eventType == "export.failed" || (eventType == "export.cleanup" && key == "") {
		q, cancel := s.database.WithContext(ctx)
		defer cancel()
		var job catalog.ExportJob
		if err := q.Select("started_at, finished_at").Where("tenant_id=? AND id=?", tenant, id).Take(&job).Error; err != nil {
			return err
		}
		if job.StartedAt != nil && job.FinishedAt != nil {
			seconds := max(0, job.FinishedAt.Sub(*job.StartedAt).Seconds())
			duration = &seconds
		}
	}
	raw, err := json.Marshal(struct {
		ExportID int64    `json:"export_id"`
		Key      string   `json:"key,omitempty"`
		Failed   bool     `json:"failed,omitempty"`
		Duration *float64 `json:"duration_seconds,omitempty"`
	}{id, key, eventType == "export.failed", duration})
	if err != nil {
		return err
	}
	if eventType == "export.failed" {
		eventType = "export.cleanup"
	}
	return outbox.Emit(ctx, s.database, outbox.Event{TenantID: tenant, AggregateType: "export_job", AggregateID: strconv.FormatInt(id, 10), EventType: eventType, Payload: raw})
}

var errExportFence = errors.New("export attempt superseded")

func (s *exportStore) PublishExport(ctx context.Context, job catalog.ExportJob, key string, digest catalog.ExportDigest) (bool, error) {
	if err := catalogQuery("Export.Publish"); err != nil {
		return false, err
	}
	if key != catalog.ExportObjectKey(job) || digest.Rows != job.RowTotal {
		return false, errors.New("invalid export publication")
	}
	err := s.database.Transaction(ctx, func(ctx context.Context) error {
		q, cancel := s.database.WithContext(ctx)
		defer cancel()
		file := struct {
			ID                                    int64
			TenantID                              int64
			Bucket, ObjectKey, OriginalName, MIME string
			SizeBytes                             int64
			SHA256, Purpose                       string
			UploadedBy                            int64
		}{
			TenantID: job.TenantID, Bucket: s.bucket, ObjectKey: key, OriginalName: job.Kind + ".csv", MIME: "text/csv", SizeBytes: digest.Size, SHA256: digest.SHA256, Purpose: "export", UploadedBy: job.RequestedBy,
		}
		if err := q.Table("file_object").Create(&file).Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		result := q.Table("export_job").Where("tenant_id=? AND id=? AND attempt=? AND status='running'", job.TenantID, job.ID, job.Attempt).
			Updates(map[string]any{"object_key": key, "file_id": file.ID, "status": "succeeded", "row_count": digest.Rows, "finished_at": now, "expires_at": now.Add(24 * time.Hour), "error_msg": ""})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return errExportFence
		}
		return s.emitExport(ctx, job.TenantID, job.ID, "export.cleanup", "")
	})
	if errors.Is(err, errExportFence) {
		return false, nil
	}
	return err == nil, err
}

func (s *exportStore) FailExport(ctx context.Context, job catalog.ExportJob) error {
	if err := catalogQuery("Export.Fail"); err != nil {
		return err
	}
	return s.database.Transaction(ctx, func(ctx context.Context) error {
		q, cancel := s.database.WithContext(ctx)
		defer cancel()
		result := q.Table("export_job").Where("tenant_id=? AND id=? AND attempt=? AND status='running'", job.TenantID, job.ID, job.Attempt).
			Updates(map[string]any{"status": "failed", "error_msg": "export attempt interrupted", "finished_at": time.Now().UTC()})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}
		return s.emitExport(ctx, job.TenantID, job.ID, "export.failed", catalog.ExportObjectKey(job))
	})
}

var _ catalog.ExportStore = (*exportStore)(nil)
