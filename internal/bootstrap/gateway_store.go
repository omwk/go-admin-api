package bootstrap

import (
	"context"
	"errors"
	"time"

	"github.com/lib/pq"
	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/gateway"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type gatewayStore struct{ database *db.DB }

type gatewayClientRow struct {
	gateway.Client `gorm:"embedded"`
	Networks       pq.StringArray `gorm:"column:ip_whitelist;type:inet[]"`
}

const gatewayClientColumns = "c.id,c.tenant_id,c.app_id,c.name,c.owner_user_id,c.status,c.ip_whitelist,c.version,c.created_at,c.updated_at"

// NewGatewayStore constructs primary-database application lifecycle persistence.
func NewGatewayStore(database *db.DB) gateway.Store { return &gatewayStore{database: database} }

func (s *gatewayStore) Transaction(ctx context.Context, fn func(context.Context) error) error {
	return s.database.Transaction(ctx, fn)
}

func (s *gatewayStore) OwnerExists(ctx context.Context, tenant, owner int64) (bool, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var count int64
	err := q.Table("sys_user u").Joins("JOIN tenant t ON t.id=u.tenant_id AND t.status='active'").
		Where("u.tenant_id=? AND u.id=? AND u.status='active' AND u.deleted_at IS NULL", tenant, owner).Count(&count).Error
	return count == 1, gatewayDBError(err)
}

func (s *gatewayStore) CreateClient(ctx context.Context, client *gateway.Client) error {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	row := gatewayClientRow{Client: *client, Networks: pq.StringArray(client.IPWhitelist)}
	if err := q.Table("api_client").Create(&row).Error; err != nil {
		return gatewayDBError(err)
	}
	*client = row.Client
	return nil
}

func (s *gatewayStore) FindClient(ctx context.Context, appID string) (gateway.Client, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var row gatewayClientRow
	err := q.Table("api_client c").Select(gatewayClientColumns).
		Joins("JOIN tenant t ON t.id=c.tenant_id AND t.status='active'").
		Where("c.app_id=?", appID).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return gateway.Client{}, errs.New(80101, 401, "应用凭证不可用")
	}
	if err != nil {
		return gateway.Client{}, gatewayDBError(err)
	}
	row.Client.IPWhitelist = []string(row.Networks)
	return row.Client, nil
}

func (s *gatewayStore) LockClient(ctx context.Context, tenant, id int64) (gateway.Client, error) {
	if !db.InTransaction(ctx) {
		return gateway.Client{}, gateway.Unavailable()
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var row gatewayClientRow
	err := q.Table("api_client c").Select(gatewayClientColumns).Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("c.tenant_id=? AND c.id=?", tenant, id).Take(&row).Error
	row.Client.IPWhitelist = []string(row.Networks)
	return row.Client, gatewayDBError(err)
}

func (s *gatewayStore) Audit(ctx context.Context, event audit.Event) error {
	return audit.Record(ctx, s.database, event)
}

func (s *gatewayStore) SetClientStatus(ctx context.Context, tenant, id int64, version int, status string) error {
	client, err := s.LockClient(ctx, tenant, id)
	if err != nil {
		return err
	}
	if client.Version != version {
		return errs.New(10409, 409, "资源版本冲突")
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	result := q.Table("api_client").Where("tenant_id=? AND id=? AND version=?", tenant, id, version).
		Updates(map[string]any{"status": status, "version": version + 1, "updated_at": time.Now().UTC()})
	return gatewayMutationError(result)
}

func gatewayDBError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return gateway.ResourceUnavailable()
	}
	return gateway.Unavailable()
}

func gatewayMutationError(result *gorm.DB) error {
	if result.Error != nil {
		return gatewayDBError(result.Error)
	}
	if result.RowsAffected != 1 {
		return gateway.ResourceUnavailable()
	}
	return nil
}

var _ gateway.Store = (*gatewayStore)(nil)
