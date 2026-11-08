package bootstrap

import (
	"context"
	"errors"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/kernel/cache"
	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/share"
	"gorm.io/gorm"
)

type shareStore struct{ database *db.DB }

// NewShareStore constructs the tenant and customer-owner pricing adapter.
func NewShareStore(database *db.DB) share.Store { return &shareStore{database: database} }

func registerTradeModules(router *gin.Engine, auth *identity.AuthService, profiles *identity.ProfileService, database *db.DB, shared *cache.Cache, cfg *config.Config) error {
	if err := registerTradeRoutes(router, auth, profiles, database, cfg); err != nil {
		return err
	}
	if err := registerShareRoutes(router, auth, profiles, database, cfg); err != nil {
		return err
	}
	sealer, err := credentialSealer(cfg)
	if err != nil {
		return err
	}
	svc, err := share.NewLinkService(share.LinkDependencies{Store: &shareStore{database: database}, Idempotency: newIdempotencyStore(database), Sealer: sealer, Cache: shared})
	if err != nil {
		return err
	}
	if err := share.RegisterLinkRoutes(router, auth, profiles, svc); err != nil {
		return err
	}
	share.RegisterPublicRoutes(router, svc, 1)
	return nil
}

func registerShareRoutes(router *gin.Engine, auth *identity.AuthService, profiles *identity.ProfileService, database *db.DB, cfg *config.Config) error {
	sealer, err := credentialSealer(cfg)
	if err != nil {
		return err
	}
	svc, err := share.NewService(share.Dependencies{Store: NewShareStore(database), Idempotency: newIdempotencyStore(database), Sealer: sealer})
	if err != nil {
		return err
	}
	return share.RegisterRoutes(router, auth, profiles, svc)
}

func (s *shareStore) Authorize(ctx context.Context, a share.Actor, code string) (context.Context, error) {
	return (&orderQueryStore{database: s.database}).Authorize(ctx, a.TenantID, a.UserID, code)
}

func (s *shareStore) Transaction(ctx context.Context, fn func(context.Context) error) error {
	return shareDBError(s.database.Transaction(ctx, fn))
}

func (s *shareStore) Snapshot(ctx context.Context, fn func(context.Context) error) error {
	return s.database.Transaction(ctx, func(tx context.Context) error {
		q, cancel := s.database.WithContext(tx)
		defer cancel()
		if err := q.Exec("SET TRANSACTION ISOLATION LEVEL REPEATABLE READ, READ ONLY").Error; err != nil {
			return err
		}
		return fn(tx)
	})
}

func (s *shareStore) NextID(ctx context.Context, entity string) (int64, error) {
	queries := map[string]string{"customer": "SELECT nextval('customer_id_seq')", "price_list": "SELECT nextval('price_list_id_seq')", "price_list_item": "SELECT nextval('price_list_item_id_seq')"}
	query, ok := queries[entity]
	if !ok {
		return 0, share.Invalid()
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var id int64
	err := q.Raw(query).Scan(&id).Error
	return id, err
}

func (s *shareStore) Audit(ctx context.Context, e audit.Event) error {
	return audit.Record(ctx, s.database, e)
}

func shareDBError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return share.NotFound()
	}
	var p *pgconn.PgError
	if errors.As(err, &p) {
		if p.Code == "23505" {
			return errs.New(errs.ErrConflict, 409, "客户编码或价目表条目重复")
		}
		if p.Code == "23514" || p.Code == "22003" {
			return share.Invalid()
		}
	}
	return err
}

var _ share.Store = (*shareStore)(nil)
