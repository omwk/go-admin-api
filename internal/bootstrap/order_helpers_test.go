package bootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/crypto"
	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/modules/ingestion"
	"github.com/omwk/go-admin-api/internal/modules/order"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm/clause"
)

type orderFixture struct {
	database                     *db.DB
	ctx                          context.Context
	upserter                     *order.Upserter
	sealer                       crypto.Sealer
	shopID, platformID, sequence int64
}

func newOrderFixture(t *testing.T) *orderFixture {
	t.Helper()
	identity := newIdentityFixture(t)
	f := &orderFixture{database: identity.database, ctx: identity.ctx}
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.Table("platform").Select("id").Where("code=?", "shopee").Take(&f.platformID).Error)
	shop := map[string]any{"tenant_id": int64(1), "platform_id": f.platformID, "name": "order-fixture", "external_shop_id": "order-fixture",
		"timezone": "UTC", "currency": "CNY", "owner_user_id": identity.user.ID, "dept_id": identity.user.DepartmentID}
	require.NoError(t, q.Table("shop").Clauses(clause.Returning{}).Create(shop).Error)
	f.shopID = shop["id"].(int64)
	var err error
	f.sealer, err = crypto.NewKeyring(map[int][]byte{1: shopRandomBytes(t, 32)}, 1)
	require.NoError(t, err)
	f.upserter, err = order.NewUpserter(f.sealer, order.MoneyPolicy{FormulaToleranceCents: 2, PaymentToleranceCents: 2})
	require.NoError(t, err)
	return f
}

func (f *orderFixture) snapshot(t *testing.T, external string) order.Snapshot {
	t.Helper()
	f.sequence++
	payload, err := json.Marshal(map[string]any{"instance": external, "observation": f.sequence})
	require.NoError(t, err)
	refs, err := rawServiceFor(t, f.database, 1, f.platformID, f.sealer).SaveRaw(f.ctx, f.shopID, "order", []ingestion.RawItem{{ExternalID: external, Payload: payload, FetchedAt: time.Now().UTC().Add(time.Duration(f.sequence) * time.Second)}})
	require.NoError(t, err)
	revision := f.sequence
	rows := make([]order.ItemInput, 3)
	for i, amount := range []int64{101, 202, 303} {
		rows[i] = order.ItemInput{ExternalItemID: fmt.Sprintf("line-%d", i+1), Title: "sale snapshot", Currency: "CNY", PriceCents: amount, Qty: 1, AmountCents: amount, DiscountEligible: true, RefundStatus: "none"}
	}
	return order.Snapshot{TenantID: 1, ShopID: f.shopID, PlatformID: f.platformID, ExternalOrderID: external, StableIdentity: true, OrderNo: "display-number",
		States: order.States{Lifecycle: "paid", Fulfillment: "unshipped", Payment: "paid", AfterSales: "none", Cancel: "none"}, Currency: "CNY",
		Money: order.Money{GoodsAmountCents: 606, DiscountCents: 100, TotalAmountCents: 506, PaidAmountCents: 506}, PaymentSettled: true, Revision: &revision,
		Source:  order.Source{RawID: refs[0].ID, FetchedAt: refs[0].FetchedAt, MappingChecksum: "order-fixture-v1"},
		Items:   &order.ItemSet{Rows: rows, Complete: true, ExpectedCount: 3, AllocationSource: "largest_remainder_v1"},
		Address: &order.AddressInput{ReceiverName: shopRandomBytes(t, 24), Phone: shopRandomBytes(t, 24), Address: shopRandomBytes(t, 48)}}
}

func (f *orderFixture) apply(s order.Snapshot) (order.Result, error) {
	var result order.Result
	err := f.database.Transaction(f.ctx, func(ctx context.Context) error {
		q, cancel := f.database.WithContext(ctx)
		defer cancel()
		var err error
		result, err = f.upserter.Apply(ctx, q, s)
		return err
	})
	return result, err
}

func (f *orderFixture) aggregate(t *testing.T) map[string]string {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	result := map[string]string{}
	for _, table := range []string{"sales_order", "order_item", "order_address", "order_status_history", "sync_cursor"} {
		var encoded string
		require.NoError(t, q.Raw(fmt.Sprintf("SELECT COALESCE(jsonb_agg(to_jsonb(r) ORDER BY id),'[]'::jsonb)::text FROM %s r", table)).Scan(&encoded).Error)
		result[table] = encoded
	}
	return result
}

func (f *orderFixture) items(t *testing.T, id int64) []order.Item {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var items []order.Item
	require.NoError(t, q.Select("id,external_item_id,amount_cents,order_discount_cents,allocation_source,deleted_at,title").Where("tenant_id=1 AND order_id=? AND deleted_at IS NULL", id).Order("external_item_id").Find(&items).Error)
	return items
}
