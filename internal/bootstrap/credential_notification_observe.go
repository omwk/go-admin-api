package bootstrap

import (
	"context"
	"log/slog"
	"time"
)

type credentialObservation struct {
	CredentialID, TenantID, ShopID, OwnerUserID int64
	Platform, Status, Kind, RefreshStrategy     string
	ConsecutiveFailures, RetryBudget            int
	ExpiresAt, RefreshAt, RunningAt             *time.Time
	Unknown                                     bool
}

func notificationLevel(row credentialObservation, now time.Time) (string, string) {
	if row.Unknown {
		return "credential.unknown", "critical"
	}
	if row.Status == "needs_reauth" {
		return "credential.needs_reauth", "critical"
	}
	if row.RunningAt != nil && now.Sub(*row.RunningAt) >= 60*time.Second {
		level := "warning"
		if now.Sub(*row.RunningAt) >= 120*time.Second {
			level = "critical"
		}
		return "credential.stuck", level
	}
	if row.ConsecutiveFailures >= 3 || row.RetryBudget == 0 {
		return "credential.failed", "critical"
	}
	if row.RefreshStrategy == "auto" && row.RefreshAt == nil {
		return "credential.protocol_pending", "warning"
	}
	if row.ExpiresAt == nil {
		return "", ""
	}
	remaining := row.ExpiresAt.Sub(now)
	if remaining <= 0 {
		return "credential.expired", "critical"
	}
	if row.RefreshStrategy == "auto" && row.Kind != "cookie" {
		if now.Sub(*row.RefreshAt) >= 120*time.Second {
			return "credential.failed", "warning"
		}
		return "", ""
	}
	if remaining <= 24*time.Hour {
		return "credential.expiring.1d", "warning"
	}
	if remaining <= 72*time.Hour {
		return "credential.expiring.3d", "warning"
	}
	return "", ""
}

func (n *credentialNotifications) observe(ctx context.Context) error {
	var after int64
	for {
		q, cancel := n.runtime.database.WithContext(ctx)
		var rows []credentialObservation
		err := q.Table("shop_credential c").Select(`c.id AS credential_id,c.tenant_id,c.shop_id,s.owner_user_id,p.code AS platform,c.status,c.kind,c.refresh_strategy,
   c.expires_at,c.refresh_at,c.consecutive_failures,c.retry_budget,
   (SELECT min(r.started_at) FROM credential_refresh_run r WHERE r.tenant_id=c.tenant_id AND r.credential_id=c.id AND r.status='running') AS running_at,
   EXISTS(SELECT 1 FROM credential_refresh_reconciliation x WHERE x.tenant_id=c.tenant_id AND x.credential_id=c.id AND x.resolved_at IS NULL) AS unknown`).
			Joins("JOIN shop s ON s.tenant_id=c.tenant_id AND s.id=c.shop_id AND s.deleted_at IS NULL").Joins("JOIN platform p ON p.id=s.platform_id").
			Where("c.id>? AND c.status<>'disabled'", after).Order("c.id").Limit(100).Find(&rows).Error
		cancel()
		if err != nil {
			return credentialDBError(err)
		}
		for _, row := range rows {
			template, severity := notificationLevel(row, n.now().UTC())
			if template != "" {
				if err := n.notify(ctx, credentialAlert{TenantID: row.TenantID, CredentialID: row.CredentialID, ShopID: row.ShopID, OwnerUserID: row.OwnerUserID, Platform: row.Platform, Template: template, Severity: severity}); err != nil {
					return err
				}
			}
			after = row.CredentialID
		}
		if len(rows) < 100 {
			return nil
		}
	}
}

func (n *credentialNotifications) poll(ctx context.Context, log *slog.Logger) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	nextObservation := time.Time{}
	for {
		now := n.now().UTC()
		if !now.Before(nextObservation) {
			if err := n.observe(ctx); err != nil && ctx.Err() == nil {
				log.ErrorContext(ctx, "credential notification observation failed", "module", "shop", "action", "notification_observe")
			}
			nextObservation = now.Add(time.Minute)
		}
		for i := 0; i < 100; i++ {
			more, err := n.deliverOne(ctx)
			if err != nil {
				if ctx.Err() == nil {
					log.ErrorContext(ctx, "notification delivery persistence failed", "module", "shop", "action", "notification_deliver")
				}
				break
			}
			if !more {
				break
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
