package bootstrap

import (
	"context"
	"errors"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/kernel/logger"
	"github.com/omwk/go-admin-api/internal/modules/billing"
)

func newBillingReconciler(database *db.DB, now func() time.Time) (*billing.Reconciler, error) {
	return billing.NewReconciler(&billingReconcileStore{database: database}, now, func(ctx context.Context, d billing.Difference) error {
		logger.From(ctx).ErrorContext(ctx, "billing reconciliation difference", "module", "billing", "action", "reconcile_difference", "code", 90401, "diff_id", d.ID, "call_id", d.CallID, "kind", d.Kind, "expected", d.Expected, "actual", d.Actual)
		return nil
	})
}

func billingSchedule(database *db.DB, next func(context.Context, time.Time) error) func(context.Context, time.Time) error {
	return func(ctx context.Context, now time.Time) error {
		return errors.Join(next(ctx, now), runBillingSchedule(ctx, database, now))
	}
}

func runBillingSchedule(ctx context.Context, database *db.DB, now time.Time) error {
	now = now.UTC()
	money, err := billing.NewService(&billingStore{database: database}, func() time.Time { return now })
	if err != nil {
		return err
	}
	for {
		n, err := money.Expire(ctx)
		if err != nil {
			return err
		}
		if n < 500 {
			break
		}
	}
	if now.Hour() < 2 {
		return nil
	}
	reports, err := newBillingReconciler(database, func() time.Time { return now })
	if err != nil {
		return err
	}
	for after := int64(0); ; {
		q, cancel := database.WithContext(ctx)
		var tenants []int64
		err := q.Table("api_client").Select("DISTINCT tenant_id").Where("tenant_id>?", after).Order("tenant_id").Limit(200).Scan(&tenants).Error
		cancel()
		if err != nil {
			return billing.Unavailable()
		}
		if len(tenants) == 0 {
			return nil
		}
		for _, tenant := range tenants {
			if _, err := reports.Run(ctx, billing.Actor{TenantID: tenant}, now.AddDate(0, 0, -1).Format("2006-01-02"), "daily"); err != nil {
				return err
			}
			after = tenant
		}
	}
}
