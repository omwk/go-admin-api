package bootstrap

import (
	"errors"

	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// bindFirstSuperRole is called only when creating an invitee or activating a registration,
// using the query from the service's first-user election transaction.
func bindFirstSuperRole(q *gorm.DB, user model.User) error {
	if !user.IsSuper {
		return nil
	}
	var role model.Role
	err := q.Select("id").Where("tenant_id=? AND code=? AND status='active'", user.TenantID, identity.SuperAdminRoleCode).
		Clauses(clause.Locking{Strength: "SHARE"}).Take(&role).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return errors.New("first administrator role unavailable")
	}
	if err != nil {
		return err
	}
	return q.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.UserRole{TenantID: user.TenantID, UserID: user.ID, RoleID: role.ID}).Error
}
