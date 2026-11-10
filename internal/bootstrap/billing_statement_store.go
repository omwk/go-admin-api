package bootstrap

import (
	"context"
	"strconv"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/modules/billing"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type billingStatementStore struct{ database *db.DB }

const statementColumns = "id,tenant_id,app_id,currency,period_start,period_end,status,total_cents,total_units,cutoff_at,source_hash,version,created_at,updated_at,issued_at,issued_by"
const statementLineColumns = "id,tenant_id,statement_id,source_key,kind,amount_cents,units,price_cents,quantity,route_version,entitlement_id,call_id,original_txn_id,original_statement_id,service_date,occurred_at"
const adjustmentColumns = "id,tenant_id,app_id,currency,original_txn_id,kind,amount_cents,diff_id,reason,evidence,actor_id,idempotency_key,request_hash,status,period_start,created_at,applied_at,ledger_txn_id"

func (s *billingStatementStore) Transaction(ctx context.Context, fn func(context.Context) error) error {
	return s.database.Transaction(ctx, fn)
}

func (s *billingStatementStore) lockApp(ctx context.Context, a billing.Actor, app string) error {
	if !db.InTransaction(ctx) {
		return billing.Unavailable()
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	if err := q.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", "billing:statement:"+strconv.FormatInt(a.TenantID, 10)+":"+app).Error; err != nil {
		return billing.Unavailable()
	}
	var count int64
	err := q.Table("api_client").Where("tenant_id=? AND app_id=? AND owner_user_id=?", a.TenantID, app, a.UserID).Count(&count).Error
	if err != nil {
		return billing.Unavailable()
	}
	if count != 1 {
		return billing.Missing()
	}
	return nil
}

func (s *billingStatementStore) BeginDraft(ctx context.Context, a billing.Actor, app string, start, at time.Time) (billing.Statement, error) {
	if err := s.lockApp(ctx, a, app); err != nil {
		return billing.Statement{}, err
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	out := billing.Statement{TenantID: a.TenantID, AppID: app, Currency: billing.Currency, PeriodStart: start, PeriodEnd: start.AddDate(0, 1, 0), Status: "draft", CutoffAt: at, Version: 1, CreatedAt: at, UpdatedAt: at}
	if err := q.Table("billing_statement").Clauses(clause.OnConflict{DoNothing: true}).Create(&out).Error; err != nil {
		return out, billing.Unavailable()
	}
	err := q.Table("billing_statement").Select(statementColumns).Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND app_id=? AND currency=? AND period_start=?", a.TenantID, app, billing.Currency, start).Take(&out).Error
	return out, billingDBError(err)
}

func (s *billingStatementStore) ClearLines(ctx context.Context, st billing.Statement) error {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	return billingDBError(q.Table("billing_statement_line").Where("tenant_id=? AND statement_id=?", st.TenantID, st.ID).Delete(&billing.StatementLine{}).Error)
}

func (s *billingStatementStore) InsertLines(ctx context.Context, st billing.Statement, rows []billing.StatementLine) error {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	for i := range rows {
		rows[i].TenantID, rows[i].StatementID = st.TenantID, st.ID
	}
	return billingDBError(q.Table("billing_statement_line").CreateInBatches(&rows, 500).Error)
}

func (s *billingStatementStore) SaveDraft(ctx context.Context, st *billing.Statement) error {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	result := q.Table("billing_statement").Where("tenant_id=? AND id=? AND status='draft' AND version=?", st.TenantID, st.ID, st.Version).Updates(map[string]any{"total_cents": st.TotalCents, "total_units": st.TotalUnits, "cutoff_at": st.CutoffAt, "source_hash": st.SourceHash, "version": st.Version + 1, "updated_at": st.CutoffAt})
	if result.Error != nil {
		return billing.Unavailable()
	}
	if result.RowsAffected != 1 {
		return billing.TerminalConflict()
	}
	st.Version++
	st.UpdatedAt = st.CutoffAt
	return nil
}

func (s *billingStatementStore) OwnedStatement(ctx context.Context, a billing.Actor, id int64) (billing.Statement, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var out billing.Statement
	query := ownedStatementQuery(q, a, "billing_statement").Select(statementColumns).Where("id=?", id)
	if db.InTransaction(ctx) {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	err := query.Take(&out).Error
	return out, billingDBError(err)
}

func ownedStatementQuery(q *gorm.DB, a billing.Actor, table string) *gorm.DB {
	return q.Table(table).Where("tenant_id=? AND app_id IN (SELECT app_id FROM api_client WHERE tenant_id=? AND owner_user_id=?)", a.TenantID, a.TenantID, a.UserID)
}

func (s *billingStatementStore) StatementLines(ctx context.Context, st billing.Statement, after int64, size int) ([]billing.StatementLine, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	out := []billing.StatementLine{}
	err := q.Table("billing_statement_line").Select(statementLineColumns).Where("tenant_id=? AND statement_id=? AND id>?", st.TenantID, st.ID, after).Order("id").Limit(size).Find(&out).Error
	return out, billingDBError(err)
}

func (s *billingStatementStore) Statements(ctx context.Context, a billing.Actor, f billing.StatementFilter) (billing.StatementPage, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	out := billing.StatementPage{Items: []billing.Statement{}, Page: f.Page, PageSize: f.PageSize}
	query := ownedStatementQuery(q, a, "billing_statement")
	if f.AppID != "" {
		query = query.Where("app_id=?", f.AppID)
	}
	if err := query.Count(&out.Total).Error; err != nil {
		return out, billing.Unavailable()
	}
	err := query.Select(statementColumns).Order("created_at DESC,id DESC").Offset((f.Page - 1) * f.PageSize).Limit(f.PageSize).Find(&out.Items).Error
	return out, billingDBError(err)
}

func (s *billingStatementStore) AuditStatement(ctx context.Context, a billing.Actor, st billing.Statement, action string) error {
	return audit.Record(ctx, s.database, audit.Event{TenantID: a.TenantID, ActorType: "user", ActorID: &a.UserID, Action: action, ResourceType: "billing_statement", ResourceID: strconv.FormatInt(st.ID, 10), After: map[string]any{"status": st.Status, "version": st.Version, "total_cents": st.TotalCents, "total_units": st.TotalUnits, "source_hash": st.SourceHash}, Result: "success"})
}

var _ billing.StatementStore = (*billingStatementStore)(nil)
