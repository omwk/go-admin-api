package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/omwk/go-admin-api/internal/kernel/db"
	identityscope "github.com/omwk/go-admin-api/internal/modules/identity/scope"
	"github.com/omwk/go-admin-api/internal/modules/share"
	"gorm.io/gorm"
)

const shareLinkColumns = "l.id,l.tenant_id,l.token_hash,l.token_prefix,l.type,l.mode,l.price_list_id,l.customer_id,l.title,l.password_hash,l.expires_at,l.max_views,l.view_count,l.allow_inquiry,l.status,l.content_version,l.created_by,l.created_at,l.revoked_at"

func shareActorUser(ctx context.Context) int64 {
	a, err := identityscope.From(ctx)
	if err != nil {
		return 0
	}
	return a.User
}

func (s *shareStore) Consistent(ctx context.Context, fn func(context.Context) error) error {
	if db.InTransaction(ctx) {
		return fn(ctx)
	}
	return s.Transaction(ctx, func(tx context.Context) error {
		q, cancel := s.database.WithContext(tx)
		defer cancel()
		if err := q.Exec("SET TRANSACTION ISOLATION LEVEL REPEATABLE READ").Error; err != nil {
			return err
		}
		return fn(tx)
	})
}

func (s *shareStore) linkBase(ctx context.Context, tenant int64) (*gorm.DB, context.CancelFunc, error) {
	if _, err := scopeRegistryQuery("Share.Management"); err != nil {
		return nil, func() {}, err
	}
	a, err := identityscope.From(ctx)
	if err != nil || a.Tenant != tenant {
		return nil, func() {}, identityscope.Denied()
	}
	q, cancel := s.database.WithContext(ctx)
	q = q.Table("share_link l").Where("l.tenant_id=?", tenant).
		Joins("LEFT JOIN customer c ON c.tenant_id=l.tenant_id AND c.id=l.customer_id").
		Where("l.customer_id IS NULL OR c.id IS NOT NULL").
		Joins("LEFT JOIN sys_user scope_owner ON scope_owner.tenant_id=l.tenant_id AND scope_owner.id=COALESCE(c.owner_user_id,l.created_by)")
	return shareOwnerScope(q, a), cancel, nil
}

func (s *shareStore) Link(ctx context.Context, tenant, id int64, lock bool) (share.Link, error) {
	q, cancel, err := s.linkBase(ctx, tenant)
	if err != nil {
		return share.Link{}, err
	}
	defer cancel()
	var row share.Link
	err = shareLock(q, "l", lock).Select(shareLinkColumns).Where("l.id=?", id).Take(&row).Error
	return row, shareDBError(err)
}

func (s *shareStore) Links(ctx context.Context, tenant int64, in share.Query) ([]share.Link, int64, error) {
	q, cancel, err := s.linkBase(ctx, tenant)
	if err != nil {
		return nil, 0, err
	}
	defer cancel()
	if in.Keyword != "" {
		q = q.Where("l.title ILIKE ?", "%"+in.Keyword+"%")
	}
	if in.Status != "" {
		q = q.Where("l.status=?", in.Status)
	}
	var total int64
	if err := q.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	rows := []share.Link{}
	err = q.Select(shareLinkColumns).Order("l.created_at DESC,l.id DESC").Offset((in.Page - 1) * in.PageSize).Limit(in.PageSize).Find(&rows).Error
	return rows, total, err
}

func (s *shareStore) InsertLink(ctx context.Context, row *share.Link, frozen share.Frozen) error {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	if err := q.Create(row).Error; err != nil {
		var p *pgconn.PgError
		if errors.As(err, &p) && p.Code == "23505" {
			return share.ErrTokenCollision
		}
		return err
	}
	raw, err := json.Marshal(frozen)
	if err != nil {
		return err
	}
	return q.Exec("INSERT INTO share_snapshot(tenant_id,share_link_id,payload,checksum,item_count,created_at) VALUES (?,?,?::jsonb,?,?,?)",
		row.TenantID, row.ID, string(raw), frozen.Content.ContentVersion, len(frozen.Content.Items), row.CreatedAt).Error
}

func (s *shareStore) RevokeLink(ctx context.Context, tenant, id int64, now time.Time) error {
	if _, err := s.Link(ctx, tenant, id, true); err != nil {
		return err
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	return q.Table("share_link").Where("tenant_id=? AND id=? AND status<>'revoked'", tenant, id).
		Updates(map[string]any{"status": "revoked", "revoked_at": now}).Error
}

func (s *shareStore) ResolveLink(ctx context.Context, tenant int64, hash string) (share.Link, error) {
	if _, err := scopeRegistryQuery("Share.Anonymous"); err != nil {
		return share.Link{}, err
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var row share.Link
	err := q.Table("share_link l").Select(shareLinkColumns).Where("l.tenant_id=? AND l.token_hash=?", tenant, hash).Take(&row).Error
	return row, shareDBError(err)
}

func (s *shareStore) Frozen(ctx context.Context, tenant, id int64) (share.Frozen, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var payload string
	err := q.Table("share_snapshot").Select("payload").Where("tenant_id=? AND share_link_id=?", tenant, id).Take(&payload).Error
	if err != nil {
		return share.Frozen{}, shareDBError(err)
	}
	var out share.Frozen
	err = json.Unmarshal([]byte(payload), &out)
	return out, err
}

var _ share.LinkStore = (*shareStore)(nil)
