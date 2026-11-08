package bootstrap

import (
	"context"
	"fmt"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/kernel/logger"
	"gorm.io/gorm"
)

func cleanupSharePrivacy(ctx context.Context, database *db.DB, now time.Time) error {
	if _, err := scopeRegistryQuery("Share.PrivacyCleanup"); err != nil {
		return err
	}
	q, cancel := database.WithContext(ctx)
	defer cancel()
	var tenants []int64
	if err := q.Raw(`SELECT tenant_id FROM customer UNION SELECT tenant_id FROM inquiry UNION SELECT tenant_id FROM share_link UNION SELECT tenant_id FROM share_visit
		UNION SELECT tenant_id FROM audit_log WHERE share_audit_retention_due(action,resource_type,created_at)`).Scan(&tenants).Error; err != nil {
		return err
	}
	for _, tenant := range tenants {
		if err := cleanupShareTenant(ctx, database, tenant, now.UTC()); err != nil {
			return err
		}
	}
	return nil
}

func cleanupShareTenant(ctx context.Context, database *db.DB, tenant int64, now time.Time) error {
	day := now.Truncate(24 * time.Hour)
	q, cancel := database.WithContext(ctx)
	defer cancel()
	var completed int64
	if err := q.Table("share_privacy_run").Where("tenant_id=? AND run_day=? AND completed_at IS NOT NULL", tenant, day).Count(&completed).Error; err != nil || completed > 0 {
		return err
	}
	for _, entity := range []string{"inquiry", "customer", "share_visit", "share_inquiry_receipt", "audit_log"} {
		for {
			var affected int64
			err := database.Transaction(ctx, func(tx context.Context) error {
				var err error
				affected, err = cleanupShareBatch(tx, database, tenant, entity, now)
				if err != nil || affected == 0 {
					return err
				}
				if err := audit.Record(tx, database, audit.Event{TenantID: tenant, ActorType: "system", Action: "share.privacy.cleanup", ResourceType: entity, ResourceID: "retention",
					After: map[string]any{"count": affected, "cutoff_at": now}, Result: "success"}); err != nil {
					return err
				}
				return recordSharePrivacyProgress(tx, database, tenant, day, entity, affected)
			})
			if err != nil {
				return err
			}
			if affected == 0 {
				break
			}
		}
	}
	completedQuery, completedCancel := database.WithContext(ctx)
	defer completedCancel()
	return completedQuery.Exec("INSERT INTO share_privacy_run(tenant_id,run_day,completed_at) VALUES(?,?,?) ON CONFLICT(tenant_id,run_day) DO UPDATE SET completed_at=EXCLUDED.completed_at", tenant, day, now).Error
}

func recordSharePrivacyProgress(ctx context.Context, database *db.DB, tenant int64, day time.Time, entity string, affected int64) error {
	q, cancel := database.WithContext(ctx)
	defer cancel()
	inquiries, customers, visits := int64(0), int64(0), int64(0)
	switch entity {
	case "inquiry":
		inquiries = affected
	case "customer":
		customers = affected
	case "share_visit":
		visits = affected
	}
	return q.Exec(`INSERT INTO share_privacy_run(tenant_id,run_day,inquiries,customers,visits) VALUES(?,?,?,?,?)
		ON CONFLICT(tenant_id,run_day) DO UPDATE SET inquiries=share_privacy_run.inquiries+EXCLUDED.inquiries,
		customers=share_privacy_run.customers+EXCLUDED.customers,visits=share_privacy_run.visits+EXCLUDED.visits`, tenant, day, inquiries, customers, visits).Error
}

func cleanupShareBatch(ctx context.Context, database *db.DB, tenant int64, entity string, now time.Time) (int64, error) {
	q, cancel := database.WithContext(ctx)
	defer cancel()
	if entity == "share_inquiry_receipt" {
		r := q.Exec("DELETE FROM share_inquiry_receipt WHERE ctid IN (SELECT ctid FROM share_inquiry_receipt WHERE tenant_id=? AND expires_at<=? LIMIT 200)", tenant, now)
		return r.RowsAffected, r.Error
	}
	predicate := ""
	args := []any{tenant}
	switch entity {
	case "inquiry":
		predicate = "pii_purged_at IS NULL AND ((converted_at IS NULL AND created_at<=?) OR converted_at<=?)"
		args = append(args, now.Add(-180*24*time.Hour), now.Add(-30*24*time.Hour))
	case "customer":
		predicate = "pii_purged_at IS NULL AND terminated_at<=? AND (status='disabled' OR deleted_at IS NOT NULL)"
		args = append(args, now.Add(-180*24*time.Hour))
	case "share_visit":
		predicate = "created_at<=?"
		args = append(args, now.Add(-90*24*time.Hour))
	case "audit_log":
		predicate = "share_audit_retention_due(action,resource_type,created_at) AND created_at<=?"
		args = append(args, now.AddDate(-3, 0, 0))
	default:
		return 0, fmt.Errorf("unsupported share retention entity")
	}
	var ids []int64
	if err := q.Table(entity).Where("tenant_id=? AND ("+predicate+")", args...).Select("id").Order("id").Limit(200).Scan(&ids).Error; err != nil || len(ids) == 0 {
		return 0, err
	}
	base := q.Table(entity).Where("tenant_id=? AND id IN ?", tenant, ids)
	var result *gorm.DB
	if entity == "inquiry" || entity == "customer" {
		result = base.Updates(shareEraseFields(entity, now))
	} else {
		result = base.Exec("DELETE FROM "+entity+" WHERE tenant_id=? AND id IN ?", tenant, ids)
	}
	return result.RowsAffected, result.Error
}

func shareEraseFields(entity string, now time.Time) map[string]any {
	fields := []string{"contact_name", "contact_phone", "contact_email", "company", "message", "items"}
	result := map[string]any{"pii_purged_at": now}
	if entity == "customer" {
		fields = []string{"contact_name", "phone", "email"}
		result["name"] = gorm.Expr("code")
	}
	for _, field := range fields {
		// Invalid authenticated envelopes preserve old structural constraints but cannot decrypt.
		result[field+"_cipher"], result[field+"_nonce"] = make([]byte, 16), make([]byte, 12)
	}
	return result
}

func sharePrivacyDailyCleanup(database *db.DB) func(context.Context, time.Time) error {
	return func(ctx context.Context, now time.Time) error {
		if logger.TraceID(ctx) == "" {
			ctx = logger.WithTraceID(ctx, "share-privacy-"+now.UTC().Format("20060102"))
		}
		err := cleanupSharePrivacy(ctx, database, now)
		if err != nil {
			logger.From(ctx).ErrorContext(ctx, "share privacy cleanup failed", "module", "share", "action", "privacy_cleanup")
		}
		return err
	}
}
