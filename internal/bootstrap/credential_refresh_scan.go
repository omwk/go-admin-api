package bootstrap

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"encoding/json"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/outbox"
	"github.com/omwk/go-admin-api/internal/modules/shop/refresh"
	"strconv"
)

func refreshIneligible(err error) bool {
	var app *errs.AppError
	return errors.Is(err, refresh.ErrIneligible) || errors.As(err, &app) && (app.Code == 20108 || app.Code == 30401 || app.Code == 20107)
}

func (r *credentialRefreshRuntime) scan(ctx context.Context) error {
	var after int64
	for {
		q, cancel := r.database.WithContext(ctx)
		var rows []struct{ ID, TenantID int64 }
		err := q.Table("shop_credential c").Select("c.id,c.tenant_id").
			Joins("JOIN credential_maintenance m ON m.tenant_id=c.tenant_id AND m.credential_id=c.id AND m.enabled").
			Where(`c.id>? AND c.refresh_strategy='auto' AND c.status IN ('active','expiring','refreshing') AND c.retry_budget>0
			AND (c.refresh_at<=? OR (c.refresh_at IS NULL AND c.expires_at<=?) OR EXISTS
			(SELECT 1 FROM credential_refresh_run r WHERE r.tenant_id=c.tenant_id AND r.credential_id=c.id AND r.status='running'))`,
				after, r.now().UTC(), r.now().UTC().Add(r.settings.MaintenanceLeadTime())).Order("c.id").Limit(100).Find(&rows).Error
		cancel()
		if err != nil {
			return credentialDBError(err)
		}
		for _, row := range rows {
			_, err := r.prepare(ctx, row.TenantID, row.ID, "schedule", nil)
			if err != nil && !refreshIneligible(err) {
				return err
			}
			after = row.ID
		}
		if len(rows) < 100 {
			return r.recoverPending(ctx)
		}
	}
}

func (r *credentialRefreshRuntime) recoverPending(ctx context.Context) error {
	q, cancel := r.database.WithContext(ctx)
	defer cancel()
	var ids []int64
	if err := q.Table("credential_refresh_run r").Select("r.id").Joins("JOIN credential_refresh_invocation i ON i.run_id=r.id AND i.tenant_id=r.tenant_id").
		Where("r.status='running' AND r.phase IN ('intent','sending','consumed')").Order("r.id").Limit(100).Find(&ids).Error; err != nil {
		return credentialDBError(err)
	}
	for _, id := range ids {
		if err := r.enqueuePending(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

func (r *credentialRefreshRuntime) enqueuePending(ctx context.Context, id int64) error {
	run, _, err := r.loadRun(ctx, id)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(struct {
		RunID int64 `json:"run_id"`
	}{id})
	if err != nil {
		return err
	}
	return r.database.Transaction(ctx, func(tx context.Context) error {
		return outbox.Emit(tx, r.database, outbox.Event{TenantID: run.TenantID, AggregateType: "credential_refresh_run", AggregateID: strconv.FormatInt(id, 10), EventType: "credential.refresh.dispatch", Payload: payload})
	})
}

func (r *credentialRefreshRuntime) poll(ctx context.Context, log *slog.Logger) {
	ticker := time.NewTicker(r.settings.Interval())
	defer ticker.Stop()
	for {
		if err := r.scan(ctx); err != nil && ctx.Err() == nil {
			log.ErrorContext(ctx, "credential maintenance scan failed", "module", "shop", "action", "refresh_scan")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
