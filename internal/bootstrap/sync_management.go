package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/ingestion"
	"github.com/omwk/go-admin-api/internal/modules/sync/repo"
	"gorm.io/gorm"
)

func recordTable(kind string) (string, string, error) {
	switch kind {
	case "dead_letter":
		return "dead_letter", "reason", nil
	case "quarantine_record":
		return "quarantine_record", "reason_code", nil
	default:
		return "", "", errs.Invalid(errors.New("invalid record kind"))
	}
}

func applySyncFilter(q *gorm.DB, p repo.PageQuery) *gorm.DB {
	if p.Status != "" {
		q = q.Where("x.status=?", p.Status)
	}
	if p.ShopID > 0 {
		q = q.Where("x.shop_id=?", p.ShopID)
	}
	return q
}

func (s *syncEngineStore) ListRuns(ctx context.Context, p repo.PageQuery) (repo.Result, error) {
	q, cancel, err := s.humanQuery(ctx, "sync_run", "SyncAdmin.ListRuns")
	if err != nil {
		return repo.Result{}, err
	}
	defer cancel()
	q = applySyncFilter(q, p)
	if p.JobID > 0 {
		q = q.Where("x.job_id=?", p.JobID)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return repo.Result{}, err
	}
	rows := []repo.Run{}
	err = q.Select("x." + strings.ReplaceAll(syncRunColumns, ",", ",x.") + ",shop.name AS shop_name,p.name AS platform_name").Joins("JOIN platform p ON p.id=shop.platform_id").Order("x.started_at DESC,x.id DESC").Limit(p.Size).Offset((p.Number - 1) * p.Size).Find(&rows).Error
	return repo.Result{Items: rows, Total: total, Page: p.Number, PageSize: p.Size}, err
}

func (s *syncEngineStore) ReadRun(ctx context.Context, id int64) (repo.Run, error) {
	q, cancel, err := s.humanQuery(ctx, "sync_run", "SyncAdmin.ReadRun")
	if err != nil {
		return repo.Run{}, err
	}
	defer cancel()
	var row repo.Run
	err = q.Session(&gorm.Session{}).Select("x."+strings.ReplaceAll(syncRunColumns, ",", ",x.")+",shop.name AS shop_name,p.name AS platform_name").Joins("JOIN platform p ON p.id=shop.platform_id").Where("x.id=?", id).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return repo.Run{}, syncMissing()
	}
	if err == nil {
		err = q.Joins("JOIN sync_error e ON e.run_id=x.id").Select("e.id,e.stage,e.error_code,e.message,e.retryable,e.raw_id,e.created_at").Where("x.id=?", id).Order("e.id DESC").Limit(100).Find(&row.Errors).Error
	}
	return row, err
}

func (s *syncEngineStore) ListRecords(ctx context.Context, kind string, p repo.PageQuery) (repo.Result, error) {
	table, reason, err := recordTable(kind)
	if err != nil {
		return repo.Result{}, err
	}
	q, cancel, err := s.humanQuery(ctx, table, "SyncAdmin.ListRecords")
	if err != nil {
		return repo.Result{}, err
	}
	defer cancel()
	q = applySyncFilter(q, p)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return repo.Result{}, err
	}
	rows := []repo.Record{}
	err = q.Select("x.id,x.shop_id,x.resource_type,x.external_id,x." + reason + " AS reason,x.status,x.created_at").Order("x.created_at DESC,x.id DESC").Limit(p.Size).Offset((p.Number - 1) * p.Size).Find(&rows).Error
	return repo.Result{Items: rows, Total: total, Page: p.Number, PageSize: p.Size}, err
}

func (s *syncEngineStore) ReplaySource(ctx context.Context, kind string, id int64) (repo.Start, error) {
	table, _, err := recordTable(kind)
	if err != nil {
		return repo.Start{}, err
	}
	column := "payload"
	if kind == "quarantine_record" {
		column = "reason_detail"
	}
	q, cancel, err := s.humanQuery(ctx, table, "SyncAdmin.ReplaySource")
	if err != nil {
		return repo.Start{}, err
	}
	defer cancel()
	var row struct{ Metadata json.RawMessage }
	err = q.Select("x."+column+" AS metadata").Where("x.id=?", id).Take(&row).Error
	if err != nil {
		return repo.Start{}, errs.New(60601, 409, "记录不可重放")
	}
	var source repo.ReplayRaw
	if json.Unmarshal(row.Metadata, &source) != nil || source.JobID <= 0 {
		return repo.Start{}, errs.New(60601, 409, "记录缺少重放元数据")
	}
	return repo.Start{JobID: source.JobID, Trigger: "retry", ReplayKind: kind, ReplayID: id}, nil
}

func (s *syncEngineStore) replayMetadata(ctx context.Context, job repo.Job, kind string, id int64) (repo.ReplayRaw, error) {
	table, _, err := recordTable(kind)
	if err != nil {
		return repo.ReplayRaw{}, err
	}
	column := "payload"
	if kind == "quarantine_record" {
		column = "reason_detail"
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var row struct{ Metadata json.RawMessage }
	err = q.Table(table).Select(column+" AS metadata").Where("id=? AND tenant_id=? AND shop_id=? AND resource_type=? AND status='pending'", id, job.TenantID, job.ShopID, job.Resource).Take(&row).Error
	if err != nil {
		return repo.ReplayRaw{}, err
	}
	var meta repo.ReplayRaw
	if err := json.Unmarshal(row.Metadata, &meta); err != nil {
		return meta, err
	}
	if meta.RawID <= 0 || meta.MappingVersion <= 0 || meta.FetchedAt.IsZero() || meta.JobID != job.ID {
		return meta, syncDenied()
	}
	return meta, nil
}

func (s *syncEngineStore) LoadReplay(ctx context.Context, run repo.Run) (json.RawMessage, ingestion.RawRef, error) {
	if err := engineQuery("SyncReplay.LoadReplay"); err != nil {
		return nil, ingestion.RawRef{}, err
	}
	a, err := s.capability(ctx, "sync.replay")
	if err != nil {
		return nil, ingestion.RawRef{}, err
	}
	if run.ID != a.RunID() || run.Stats.ReplayID <= 0 {
		return nil, ingestion.RawRef{}, syncDenied()
	}
	work, err := s.LoadWork(ctx, a.JobID(), a.RunID())
	if err != nil {
		return nil, ingestion.RawRef{}, err
	}
	if work.Run.Stats.ReplayID != run.Stats.ReplayID || work.Run.Stats.ReplayKind != run.Stats.ReplayKind {
		return nil, ingestion.RawRef{}, syncDenied()
	}
	job := repo.Job{ID: a.JobID(), TenantID: a.TenantID(), ShopID: a.ShopID(), Resource: a.Resource()}
	meta, err := s.replayMetadata(ctx, job, run.Stats.ReplayKind, run.Stats.ReplayID)
	if err != nil {
		return nil, ingestion.RawRef{}, err
	}
	if meta.MappingVersion != a.MappingVersion() {
		return nil, ingestion.RawRef{}, syncDenied()
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var row struct {
		ID            int64
		ExternalID    string
		PayloadSHA256 string
		Payload       json.RawMessage
	}
	err = q.Table("raw_payload").Select("id,external_id,payload_sha256,payload").Where("tenant_id=? AND platform_id=? AND shop_id=? AND resource_type=? AND id=? AND fetched_at=?", a.TenantID(), work.Job.PlatformID, a.ShopID(), a.Resource(), meta.RawID, meta.FetchedAt).Take(&row).Error
	if err == nil && a.Resource() == "order" {
		opened, openErr := s.rawProtector.Open(ingestion.ProtectedRawRef{RawIdentity: ingestion.RawIdentity{TenantID: a.TenantID(), PlatformID: work.Job.PlatformID}, ShopID: a.ShopID(), ID: row.ID, ExternalID: row.ExternalID, PayloadHash: row.PayloadSHA256, FetchedAt: meta.FetchedAt}, row.Payload)
		if openErr != nil {
			return nil, ingestion.RawRef{}, openErr
		}
		row.ExternalID, row.Payload = opened.ExternalID, opened.Payload
		clear(opened.RequestMeta)
	}
	return row.Payload, ingestion.RawRef{ID: row.ID, ExternalID: row.ExternalID, FetchedAt: meta.FetchedAt}, err
}
