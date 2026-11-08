package bootstrap

import (
	"context"
	"strings"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	kernelscope "github.com/omwk/go-admin-api/internal/kernel/scope"
	identityscope "github.com/omwk/go-admin-api/internal/modules/identity/scope"
	"github.com/omwk/go-admin-api/internal/modules/share"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *shareStore) base(ctx context.Context, tenant int64, entity, name string, includeDeleted ...bool) (*gorm.DB, context.CancelFunc, error) {
	if _, err := scopeRegistryQuery(name); err != nil {
		return nil, func() {}, err
	}
	a, err := identityscope.From(ctx)
	if err != nil || a.Tenant != tenant {
		return nil, func() {}, identityscope.Denied()
	}
	q, cancel := s.database.WithContext(ctx)
	deleted := len(includeDeleted) > 0 && includeDeleted[0]
	switch entity {
	case "customer":
		q = q.Table("customer c").Where("c.tenant_id=?", tenant).
			Joins("LEFT JOIN sys_user scope_owner ON scope_owner.tenant_id=c.tenant_id AND scope_owner.id=c.owner_user_id")
		if !deleted {
			q = q.Where("c.deleted_at IS NULL")
		}
	case "price_list":
		q = q.Table("price_list p").Where("p.tenant_id=?", tenant).
			Joins("LEFT JOIN customer c ON c.tenant_id=p.tenant_id AND c.id=p.customer_id").
			Where("p.type='default' OR c.id IS NOT NULL").
			Joins("LEFT JOIN sys_user scope_owner ON scope_owner.tenant_id=p.tenant_id AND scope_owner.id=CASE WHEN p.type='customer' THEN c.owner_user_id ELSE p.created_by END")
		if !deleted {
			q = q.Where("p.deleted_at IS NULL AND (p.type='default' OR c.deleted_at IS NULL)")
		}
	default:
		cancel()
		return nil, func() {}, share.Invalid()
	}
	return shareOwnerScope(q, a), cancel, nil
}

func shareOwnerScope(q *gorm.DB, a identityscope.Access) *gorm.DB {
	d := kernelscope.Declaration{Alias: "scope_owner", OwnerColumn: "id", DepartmentColumn: "dept_id"}
	parts, args := []string{}, []any{}
	for _, role := range a.Roles {
		if role.Kind == "all" {
			return q
		}
		predicate, values := rolePredicate(d, a, role)
		parts = append(parts, "("+predicate+")")
		args = append(args, values...)
	}
	if len(parts) == 0 {
		return q.Where("1=0")
	}
	return q.Where("("+strings.Join(parts, " OR ")+")", args...)
}

func (s *shareStore) ReplayVisible(ctx context.Context, tenant int64, entity string, id int64, deleted bool) error {
	q, cancel, err := s.base(ctx, tenant, entity, "Trade.Replay", deleted)
	if err != nil {
		return err
	}
	defer cancel()
	alias := "p"
	if entity == "customer" {
		alias = "c"
	}
	var found int64
	return shareDBError(q.Select(alias+".id").Where(alias+".id=?", id).Take(&found).Error)
}

func (s *shareStore) ValidateOwner(ctx context.Context, tenant, owner int64) error {
	a, err := identityscope.From(ctx)
	if err != nil || a.Tenant != tenant {
		return identityscope.Denied()
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var id int64
	err = shareOwnerScope(q.Table("sys_user scope_owner").Where("scope_owner.tenant_id=? AND scope_owner.id=? AND scope_owner.deleted_at IS NULL AND scope_owner.status='active'", tenant, owner), a).
		Select("scope_owner.id").Take(&id).Error
	if err != nil {
		return shareDBError(err)
	}
	return nil
}

func shareLock(q *gorm.DB, alias string, lock bool) *gorm.DB {
	if lock {
		return q.Clauses(clause.Locking{Strength: "UPDATE", Table: clause.Table{Name: alias}})
	}
	return q
}

func shareChanged(result *gorm.DB) error {
	if result.Error != nil {
		return shareDBError(result.Error)
	}
	if result.RowsAffected != 1 {
		return errs.New(errs.ErrVersionConflict, 409, "客户或价目表版本冲突")
	}
	return nil
}

func shareFilter(q *gorm.DB, alias string, in share.Query) *gorm.DB {
	if in.Keyword != "" {
		q = q.Where(alias+".name ILIKE ?", "%"+in.Keyword+"%")
	}
	if in.Status != "" {
		q = q.Where(alias+".status=?", in.Status)
	}
	return q
}

func shareOrder(alias, sort string) string {
	return map[string]string{"-created_at": alias + ".created_at DESC," + alias + ".id DESC", "created_at": alias + ".created_at," + alias + ".id",
		"name": alias + ".name," + alias + ".id", "-name": alias + ".name DESC," + alias + ".id DESC"}[sort]
}
