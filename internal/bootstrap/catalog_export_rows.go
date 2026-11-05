package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/catalog"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *exportStore) BeginExport(ctx context.Context, tenant, id int64) (job catalog.ExportJob, err error) {
	if err := catalogQuery("Export.Begin"); err != nil {
		return job, err
	}
	err = s.database.Transaction(ctx, func(ctx context.Context) error {
		q, cancel := s.database.WithContext(ctx)
		defer cancel()
		if err := lockExportAdmission(q); err != nil {
			return err
		}
		err := q.Select(exportColumns).Where("tenant_id=? AND id=?", tenant, id).Clauses(clause.Locking{Strength: "UPDATE"}).Take(&job).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return catalog.ExportNotFound()
		}
		if err != nil || job.Status == "succeeded" || job.Status == "expired" {
			return err
		}
		if job.Status == "running" && job.StartedAt != nil && time.Since(*job.StartedAt) < catalog.ExportBudget {
			return errors.New("export attempt still running")
		}
		if err := checkExportAdmission(q, tenant, job.RequestedBy, job.ID, false); err != nil {
			return err
		}
		if err := s.authorizeExport(ctx, job); err != nil {
			return err
		}
		var snapshot catalog.ExportSnapshot
		if err := json.Unmarshal(job.Filters, &snapshot); err != nil || snapshot.FieldsVersion != 1 {
			return errors.New("invalid export snapshot")
		}
		previous := job.Attempt
		job.Attempt++
		now := time.Now().UTC()
		job.Status, job.StartedAt, job.FinishedAt = "running", &now, nil
		if previous == 0 {
			total, err := materializeExport(q, job, snapshot)
			if err != nil {
				return err
			}
			job.RowTotal = total
		} else {
			result := q.Exec(`INSERT INTO export_job_row(export_id,attempt,ordinal,projection,sort_key,generated_at)
SELECT export_id,?,ordinal,projection,sort_key,generated_at FROM export_job_row WHERE export_id=? AND attempt=?`, job.Attempt, job.ID, previous)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != job.RowTotal {
				return errors.New("export retry snapshot incomplete")
			}
		}
		return q.Table("export_job").Where("tenant_id=? AND id=?", tenant, id).Updates(map[string]any{
			"status": "running", "attempt": job.Attempt, "row_total": job.RowTotal, "row_count": 0,
			"started_at": now, "finished_at": nil, "error_msg": "",
		}).Error
	})
	return job, err
}

func (s *exportStore) authorizeExport(ctx context.Context, job catalog.ExportJob) error {
	if _, err := newAdminStore(s.database).Authorize(ctx, job.TenantID, job.RequestedBy); err != nil {
		return err
	}
	profiles, err := identity.NewProfileService(newProfileStore(s.database))
	if err != nil {
		return err
	}
	actor, err := profiles.Me(ctx, job.TenantID, job.RequestedBy)
	if err != nil {
		return err
	}
	if !slices.Contains(actor.Permissions, permission.ProductSPUExport) {
		return errs.New(errs.ErrPermissionDenied, 403, "无导出权限")
	}
	return nil
}

func materializeExport(q *gorm.DB, job catalog.ExportJob, snapshot catalog.ExportSnapshot) (int64, error) {
	// One INSERT SELECT fixes both membership and every projected value in one MVCC statement.
	order := productOrder(snapshot.Sort)
	if order == "" {
		return 0, errors.New("invalid export sort")
	}
	base := productFilterQuery(q.Session(&gorm.Session{NewDB: true}).Table("product_spu p"), job.TenantID,
		catalog.ProductListQuery{ProductFilter: snapshot.Filters})
	projection := `jsonb_build_object('id',p.id,'title',p.title,'price_min_cents',p.price_min_cents,
'price_max_cents',p.price_max_cents,'sku_count',p.sku_count,'category_id',p.category_id,
'category',COALESCE(c.name,''),'status',p.status,'updated_at',p.updated_at)`
	selectSQL := base.Joins("LEFT JOIN category c ON c.tenant_id=p.tenant_id AND c.id=p.category_id").
		Select("?, ?, row_number() OVER (ORDER BY "+order+"), "+projection+
			", jsonb_build_object('id',p.id,'title',p.title,'price_min_cents',p.price_min_cents,'updated_at',p.updated_at), statement_timestamp()", job.ID, job.Attempt).
		Session(&gorm.Session{DryRun: true}).Find(&[]catalog.ExportRow{}).Statement
	result := q.Exec("INSERT INTO export_job_row(export_id,attempt,ordinal,projection,sort_key,generated_at) "+selectSQL.SQL.String(), selectSQL.Vars...)
	return result.RowsAffected, result.Error
}

func (s *exportStore) ExportRows(ctx context.Context, job catalog.ExportJob, ordinal int64) ([]catalog.ExportRow, error) {
	if err := catalogQuery("Export.Rows"); err != nil {
		return nil, err
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	rows := []catalog.ExportRow{}
	err := q.Table("export_job_row r").Select("r.ordinal, r.projection, r.generated_at").
		Where("r.export_id=? AND r.attempt=? AND r.ordinal>?", job.ID, job.Attempt, ordinal).
		Where("EXISTS (SELECT 1 FROM export_job j WHERE j.id=r.export_id AND j.tenant_id=?)", job.TenantID).
		Order("r.ordinal").Limit(1000).Find(&rows).Error
	return rows, err
}
