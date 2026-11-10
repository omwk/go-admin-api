package bootstrap

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/modules/billing"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type billingStore struct{ database *db.DB }

const billingWalletColumns = "w.id,w.tenant_id,w.app_id,w.currency,w.balance_cents,w.frozen_cents,w.version,w.status,w.updated_at"
const billingTxnColumns = "id,tenant_id,wallet_id,type,amount_cents,balance_delta_cents,frozen_delta_cents,balance_after_cents,frozen_after_cents,version_after,call_id,idempotency_key,request_hash,actor_id,note,occurred_at,biz_type,biz_id,service_date"

func (s *billingStore) Transaction(ctx context.Context, fn func(context.Context) error) error {
	return s.database.Transaction(ctx, fn)
}

func (s *billingStore) OpenWallet(ctx context.Context, a billing.Actor, app string, at time.Time) (billing.Wallet, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var count int64
	if err := q.Table("api_client").Where("tenant_id=? AND owner_user_id=? AND app_id=? AND status='active'", a.TenantID, a.UserID, app).Count(&count).Error; err != nil {
		return billing.Wallet{}, billing.Unavailable()
	}
	if count != 1 {
		return billing.Wallet{}, billing.Missing()
	}
	w := billing.Wallet{TenantID: a.TenantID, AppID: app, Currency: billing.Currency, Status: "active", Version: 1, UpdatedAt: at}
	if err := q.Table("wallet").Clauses(clause.OnConflict{DoNothing: true}).Create(&w).Error; err != nil {
		return w, billing.Unavailable()
	}
	return s.Wallet(ctx, a.TenantID, app)
}

func (s *billingStore) OwnedWallet(ctx context.Context, a billing.Actor, id int64) (billing.Wallet, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var out billing.Wallet
	err := ownedWalletQuery(q, a).Select(billingWalletColumns).Where("w.id=?", id).Take(&out).Error
	return out, billingDBError(err)
}

func ownedWalletQuery(q *gorm.DB, a billing.Actor) *gorm.DB {
	return q.Table("wallet w").Joins("JOIN api_client c ON c.tenant_id=w.tenant_id AND c.app_id=w.app_id").Where("w.tenant_id=? AND c.owner_user_id=?", a.TenantID, a.UserID)
}

func (s *billingStore) Wallet(ctx context.Context, tenant int64, app string) (billing.Wallet, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var out billing.Wallet
	err := q.Table("wallet w").Select(billingWalletColumns).Where("w.tenant_id=? AND w.app_id=? AND w.currency=?", tenant, app, billing.Currency).Take(&out).Error
	return out, billingDBError(err)
}

func (s *billingStore) Wallets(ctx context.Context, a billing.Actor) ([]billing.Wallet, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	out := []billing.Wallet{}
	err := ownedWalletQuery(q, a).Select(billingWalletColumns).Order("w.id DESC").Limit(100).Find(&out).Error
	return out, billingDBError(err)
}

func (s *billingStore) Txns(ctx context.Context, a billing.Actor, id int64) ([]billing.Txn, error) {
	if _, err := s.OwnedWallet(ctx, a, id); err != nil {
		return nil, err
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	out := []billing.Txn{}
	err := q.Table("wallet_txn").Select(billingTxnColumns).Where("tenant_id=? AND wallet_id=?", a.TenantID, id).Order("occurred_at DESC,id DESC").Limit(100).Find(&out).Error
	return out, billingDBError(err)
}

func (s *billingStore) AuditCredit(ctx context.Context, a billing.Actor, before, after billing.Wallet, action string) error {
	if after.Replayed {
		return nil
	}
	return audit.Record(ctx, s.database, audit.Event{TenantID: a.TenantID, ActorType: "user", ActorID: &a.UserID, Action: action, ResourceType: "wallet", ResourceID: strconv.FormatInt(after.ID, 10), Before: balanceAudit(before), After: balanceAudit(after), Result: "success"})
}

func balanceAudit(w billing.Wallet) map[string]any {
	return map[string]any{"balance_cents": w.BalanceCents, "frozen_cents": w.FrozenCents, "currency": w.Currency}
}

func billingDBError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return billing.Missing()
	}
	return billing.Unavailable()
}

var _ billing.Store = (*billingStore)(nil)
