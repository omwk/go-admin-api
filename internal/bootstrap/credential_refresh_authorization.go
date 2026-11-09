package bootstrap

import (
	"context"
	"slices"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/scope"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
	"github.com/omwk/go-admin-api/internal/modules/shop/model"
	"gorm.io/gorm"
)

func (r *credentialRefreshRuntime) authorizeActor(ctx context.Context, tenant, actor, shopID int64) error {
	profiles, err := identity.NewProfileService(newProfileStore(r.database))
	if err != nil {
		return err
	}
	p, err := profiles.Me(ctx, tenant, actor)
	if err != nil {
		return err
	}
	if !p.User.IsSuper && !slices.Contains(p.Permissions, permission.ShopCredentialRotate) {
		return scope.Denied()
	}
	human, err := newAdminStore(r.database).Authorize(ctx, tenant, actor)
	if err != nil {
		return err
	}
	_, err = newShopStore(r.database).Shop(human, tenant, shopID)
	return err
}

func (r *credentialRefreshRuntime) authorizeInvocation(ctx context.Context, inv *refreshInvocation, trigger string, initial bool) error {
	q, cancel := r.database.WithContext(ctx)
	defer cancel()
	var shop model.Shop
	if err := q.Select("id,tenant_id,owner_user_id,status,deleted_at").Where("tenant_id=? AND id=?", inv.TenantID, inv.ShopID).Take(&shop).Error; err != nil {
		return credentialDBError(err)
	}
	if shop.DeletedAt.Valid || shop.Status == "disabled" {
		return scope.Denied()
	}
	if initial {
		inv.OwnerUserID = shop.OwnerUserID
	}
	if inv.OwnerUserID != shop.OwnerUserID {
		return scope.Denied()
	}
	adapter, err := r.adapter(ctx, inv.TenantID, inv.CredentialID)
	if err != nil {
		return err
	}
	if trigger == "manual" {
		if inv.ActorID == nil {
			return scope.Denied()
		}
		return r.authorizeActor(ctx, inv.TenantID, *inv.ActorID, inv.ShopID)
	}
	protocol := adapter.RefreshProtocol()
	if !protocol.Verified || !protocol.ExclusiveGrant {
		return errs.New(30401, 400, "刷新协议或独立授权待核验")
	}
	var approved struct {
		Generation  int64
		OwnerUserID int64
		Enabled     bool
		Strategy    string
	}
	err = q.Table("credential_maintenance m").Select("m.generation,m.owner_user_id,m.enabled,c.refresh_strategy AS strategy").
		Joins("JOIN shop_credential c ON c.tenant_id=m.tenant_id AND c.id=m.credential_id").
		Where("m.tenant_id=? AND m.credential_id=?", inv.TenantID, inv.CredentialID).Take(&approved).Error
	if err != nil || !approved.Enabled || approved.Strategy != "auto" || approved.OwnerUserID != shop.OwnerUserID {
		return scope.Denied()
	}
	if initial {
		inv.MaintenanceGeneration = &approved.Generation
	}
	if inv.MaintenanceGeneration == nil || *inv.MaintenanceGeneration != approved.Generation {
		return scope.Denied()
	}
	return nil
}

func (s *shopStore) recordCredentialMaintenance(ctx context.Context, row model.Credential, audit model.CredentialAudit) error {
	q, cancel := s.raw(ctx)
	defer cancel()
	if row.RefreshStrategy != "auto" || row.Status == "disabled" {
		return credentialDBError(q.Table("credential_maintenance").Where("tenant_id=? AND credential_id=?", row.TenantID, row.ID).
			Updates(map[string]any{"enabled": false, "generation": gorm.Expr("generation+1")}).Error)
	}
	if audit.ActorID == nil {
		return scope.Denied()
	}
	var owner int64
	if err := q.Table("shop").Select("owner_user_id").Where("tenant_id=? AND id=?", row.TenantID, row.ShopID).Take(&owner).Error; err != nil {
		return credentialDBError(err)
	}
	return credentialDBError(q.Exec(`INSERT INTO credential_maintenance(tenant_id,credential_id,approved_by,owner_user_id)
		VALUES (?,?,?,?) ON CONFLICT (tenant_id,credential_id) DO UPDATE SET approved_by=excluded.approved_by,
		owner_user_id=excluded.owner_user_id,enabled=true,generation=credential_maintenance.generation+1,approved_at=now()`,
		row.TenantID, row.ID, *audit.ActorID, owner).Error)
}
