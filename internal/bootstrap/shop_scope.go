package bootstrap

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	kernelscope "github.com/omwk/go-admin-api/internal/kernel/scope"
	identitymodel "github.com/omwk/go-admin-api/internal/modules/identity/model"
	identityscope "github.com/omwk/go-admin-api/internal/modules/identity/scope"
	shopdomain "github.com/omwk/go-admin-api/internal/modules/shop"
	shopmodel "github.com/omwk/go-admin-api/internal/modules/shop/model"
	"github.com/omwk/go-admin-api/internal/modules/shop/repo"
	"gorm.io/gorm"
)

type shopStore struct {
	database *db.DB
	raw      func(context.Context) (*gorm.DB, context.CancelFunc)
}

var _ repo.Store = (*shopStore)(nil)

func newShopStore(database *db.DB) *shopStore {
	return &shopStore{database: database, raw: database.WithContext}
}

// Transaction keeps the business mutation and idempotency result on one
// database commit boundary.
func (s *shopStore) Transaction(ctx context.Context, fn func(context.Context) error) error {
	return shopDBError(s.database.Transaction(ctx, fn))
}

func shopScope(ctx context.Context, row any, q *gorm.DB) (*gorm.DB, error) {
	root, ok := row.(interface{ TableName() string })
	if !ok {
		return nil, identityscope.Denied()
	}
	registry, err := scopeRegistry()
	if err != nil {
		return nil, err
	}
	d, err := registry.Lookup(root.TableName())
	if err != nil {
		return nil, err
	}
	if d.Global {
		return q, nil
	}
	a, err := identityscope.From(ctx)
	if err != nil {
		return nil, err
	}
	q = q.Where(d.Alias+"."+d.TenantColumn+" = ?", a.Tenant)
	if d.DeletedColumn != "" {
		q = q.Where(d.Alias + "." + d.DeletedColumn + " IS NULL")
	}
	clauses, args := []string{}, []any{}
	for _, role := range a.Roles {
		// Authorize supplies all scope for verified super users without requiring stored roles.
		if role.Kind == "all" {
			return q, nil
		}
		predicate, values := shopRolePredicate(d, a, role)
		clauses = append(clauses, "("+predicate+")")
		args = append(args, values...)
	}
	if len(clauses) == 0 {
		return q.Where("1 = 0"), nil
	}
	return q.Where("("+strings.Join(clauses, " OR ")+")", args...), nil
}

func shopRolePredicate(d kernelscope.Declaration, a identityscope.Access, role identityscope.Role) (string, []any) {
	switch role.Kind {
	case "all":
		return "", nil
	case "self":
		return d.Alias + "." + d.OwnerColumn + " = ?", []any{a.User}
	case "dept":
		return d.Alias + "." + d.DepartmentColumn + " = ?", []any{a.Department}
	case "custom":
		if len(role.Departments) == 0 {
			return "1 = 0", nil
		}
		return d.Alias + "." + d.DepartmentColumn + " IN ?", []any{role.Departments}
	case "dept_and_child":
		if a.Path == "" {
			return "1 = 0", nil
		}
		escaped := strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(a.Path)
		return d.Alias + "." + d.DepartmentColumn + " IN (SELECT scope_dept.id FROM sys_dept scope_dept WHERE scope_dept.tenant_id = ? AND (scope_dept.path = ? OR scope_dept.path LIKE ? || '.%'))", []any{a.Tenant, a.Path, escaped}
	default:
		return "1 = 0", nil
	}
}

func (s *shopStore) query(ctx context.Context, tenant int64, row any, method string) (*db.ScopedQuery, context.CancelFunc) {
	root, ok := row.(interface{ TableName() string })
	registry, registryErr := scopeRegistry()
	var declaration kernelscope.QueryDeclaration
	var declarationErr error
	if registryErr == nil {
		declaration, declarationErr = registry.Query(method)
	} else {
		declarationErr = registryErr
	}
	allowed := ok && declarationErr == nil && len(declaration.Roots) == 1 && declaration.Roots[0].Entity == root.TableName()
	access, accessErr := identityscope.From(ctx)
	if !allowed || accessErr != nil || access.Tenant != tenant {
		ctx = db.WithScope(ctx, func(context.Context, any, *gorm.DB) (*gorm.DB, error) { return nil, identityscope.Denied() })
	} else {
		ctx = db.WithScope(ctx, shopScope)
	}
	q, cancel := s.raw(ctx)
	return db.BindScoped(ctx, row, q, cancel), cancel
}

func (s *shopStore) Platforms(ctx context.Context) ([]shopmodel.Platform, error) {
	registry, err := scopeRegistry()
	if err != nil {
		return nil, err
	}
	if _, err := registry.Query("Shop.Platforms"); err != nil {
		return nil, err
	}
	q, cancel := s.raw(ctx)
	defer cancel()
	rows := []shopmodel.Platform{}
	queryErr := q.Select("id, code, name, auth_type, capabilities, config_schema, status").Order("id asc").Find(&rows).Error
	return rows, queryErr
}

const shopColumns = "shop.id, shop.tenant_id, shop.platform_id, shop.name, shop.external_shop_id, shop.region, shop.timezone, shop.currency, shop.owner_user_id, shop.dept_id, shop.status, shop.settings, shop.last_sync_at, shop.version, shop.created_at, shop.updated_at, shop.deleted_at, platform.name AS platform_name"

func (s *shopStore) Shops(ctx context.Context, tenant int64, page repo.Page) ([]repo.ShopRow, int64, error) {
	q, cancel := s.query(ctx, tenant, &shopmodel.Shop{}, "Shop.Shops")
	defer cancel()
	base := q.Joins("JOIN platform ON platform.id = shop.platform_id")
	var total int64
	if err := base.Fork().Distinct("shop.id").Count(&total); err != nil {
		return nil, 0, err
	}
	order, ok := map[string]string{"-created_at": "shop.created_at desc, shop.id desc", "created_at": "shop.created_at asc, shop.id asc", "name": "shop.name asc, shop.id asc", "-name": "shop.name desc, shop.id desc", "id": "shop.id asc", "-id": "shop.id desc"}[page.Sort]
	if !ok {
		return nil, 0, errors.New("unsupported shop sort")
	}
	rows := []repo.ShopRow{}
	err := base.Fork().Select(shopColumns).Distinct().Order(order).Limit(page.Size).Offset((page.Number - 1) * page.Size).Find(&rows)
	return rows, total, shopDBError(err)
}

func (s *shopStore) Shop(ctx context.Context, tenant, id int64) (repo.ShopRow, error) {
	q, cancel := s.query(ctx, tenant, &shopmodel.Shop{}, "Shop.Shop")
	defer cancel()
	var row repo.ShopRow
	err := q.Joins("JOIN platform ON platform.id = shop.platform_id").Select(shopColumns).Where("shop.id = ?", id).Take(&row)
	return row, shopDBError(err)
}

func (s *shopStore) ValidateAssignment(ctx context.Context, tenant, owner, dept int64) error {
	if _, err := scopeRegistryQuery("Shop.ValidateAssignment"); err != nil {
		return err
	}
	a, err := identityscope.From(ctx)
	if err != nil || a.Tenant != tenant || owner <= 0 || dept <= 0 {
		return errs.New(errs.ErrDataScopeDenied, 403, "超出数据权限")
	}
	q, cancel := s.raw(ctx)
	defer cancel()
	var user identitymodel.User
	if err := q.Select("id").Where("tenant_id = ? AND id = ? AND status = ?", tenant, owner, "active").Take(&user).Error; err != nil {
		return errs.New(errs.ErrDataScopeDenied, 403, "超出数据权限")
	}
	var department identitymodel.Department
	if err := q.Select("id, path").Where("tenant_id = ? AND id = ? AND status = ?", tenant, dept, "active").Take(&department).Error; err != nil {
		return errs.New(errs.ErrDataScopeDenied, 403, "超出数据权限")
	}
	for _, role := range a.Roles {
		switch role.Kind {
		case "all":
			return nil
		case "self":
			if owner == a.User {
				return nil
			}
		case "dept":
			if dept == a.Department {
				return nil
			}
		case "custom":
			for _, allowed := range role.Departments {
				if allowed == dept {
					return nil
				}
			}
		case "dept_and_child":
			if a.Path != "" && (department.Path == a.Path || strings.HasPrefix(department.Path, a.Path+".")) {
				return nil
			}
		}
	}
	return errs.New(errs.ErrDataScopeDenied, 403, "超出数据权限")
}

func scopeRegistryQuery(name string) (kernelscope.QueryDeclaration, error) {
	registry, err := scopeRegistry()
	if err != nil {
		return kernelscope.QueryDeclaration{}, err
	}
	return registry.Query(name)
}

func (s *shopStore) CreateShop(ctx context.Context, row *shopmodel.Shop) error {
	q, cancel := s.query(ctx, row.TenantID, row, "Shop.CreateShop")
	defer cancel()
	return shopDBError(q.Select("tenant_id", "platform_id", "name", "external_shop_id", "region", "timezone", "currency", "owner_user_id", "dept_id", "status", "settings").Create(row))
}

func (s *shopStore) UpdateShop(ctx context.Context, tenant, id int64, version int, values map[string]any) error {
	q, cancel := s.query(ctx, tenant, &shopmodel.Shop{}, "Shop.UpdateShop")
	defer cancel()
	err := q.Where("shop.id = ? AND shop.version = ?", id, version).Updates(values)
	if err != nil {
		return shopDBError(err)
	}
	if q.RowsAffected() > 0 {
		return nil
	}
	probe, probeCancel := s.query(ctx, tenant, &shopmodel.Shop{}, "Shop.Shop")
	defer probeCancel()
	var row shopmodel.Shop
	if err := probe.Select("shop.id").Where("shop.id = ?", id).Take(&row); err == nil {
		return errs.New(errs.ErrVersionConflict, 409, "店铺版本冲突")
	}
	return shopdomain.NotFound()
}

func (s *shopStore) DeleteShop(ctx context.Context, tenant, id int64) error {
	q, cancel := s.query(ctx, tenant, &shopmodel.Shop{}, "Shop.DeleteShop")
	defer cancel()
	if err := q.Where("shop.id = ?", id).Delete(&shopmodel.Shop{}); err != nil {
		return shopDBError(err)
	}
	if q.RowsAffected() == 0 {
		return shopdomain.NotFound()
	}
	return nil
}

func shopDBError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return shopdomain.NotFound()
	}
	var postgresErr *pgconn.PgError
	if errors.As(err, &postgresErr) && postgresErr.Code == "23505" {
		return errs.New(errs.ErrShopDuplicate, 409, "同平台店铺已存在")
	}
	return err
}
