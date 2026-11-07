package order

import (
	"errors"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const orderColumns = "id,tenant_id,shop_id,platform_id,external_order_id,order_no,lifecycle_status,fulfillment_status,payment_status,after_sales_status,cancel_status,platform_status,currency,goods_amount_cents,shipping_fee_cents,discount_cents,total_amount_cents,paid_amount_cents,refunded_amount_cents,formula_delta_cents,payment_delta_cents,payment_settled,item_count,last_refunded_at,refund_rejection_code,revision,version,raw_id,source_fetched_at,mapping_checksum,content_hash,placed_at,synced_at,created_at,updated_at,deleted_at"
const orderMutableColumns = "order_no,lifecycle_status,fulfillment_status,payment_status,after_sales_status,cancel_status,platform_status,currency,goods_amount_cents,shipping_fee_cents,discount_cents,total_amount_cents,paid_amount_cents,refunded_amount_cents,formula_delta_cents,payment_delta_cents,payment_settled,item_count,last_refunded_at,refund_rejection_code,revision,version,raw_id,source_fetched_at,mapping_checksum,content_hash,placed_at,synced_at,updated_at"

func orderRow(old Order, s Snapshot, hash string, deltas Deltas, now time.Time) Order {
	row := Order{ID: old.ID, TenantID: s.TenantID, ShopID: s.ShopID, PlatformID: s.PlatformID, ExternalOrderID: s.ExternalOrderID,
		OrderNo: s.OrderNo, States: s.States, PlatformStatus: s.PlatformStatus, Currency: s.Currency, Money: s.Money,
		FormulaDeltaCents: deltas.Formula, PaymentDeltaCents: deltas.Payment, PaymentSettled: s.PaymentSettled, ItemCount: old.ItemCount,
		LastRefundedAt: s.LastRefundedAt, RefundRejectionCode: s.RefundRejectionCode, Revision: s.Revision, Version: old.Version + 1,
		RawID: s.Source.RawID, SourceFetchedAt: s.Source.FetchedAt, MappingChecksum: s.Source.MappingChecksum, ContentHash: hash,
		PlacedAt: s.PlacedAt, SyncedAt: now, UpdatedAt: now}
	if s.Items != nil {
		row.ItemCount = len(s.Items.Rows)
	}
	return row
}

func saveOrder(tx *gorm.DB, row *Order, old Order) error {
	if old.ID == 0 {
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(row)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return versionConflict()
		}
		return nil
	}
	result := tx.Model(&Order{}).Where("tenant_id=? AND id=? AND version=?", old.TenantID, old.ID, old.Version).
		Select(strings.Split(orderMutableColumns, ",")).Updates(row)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return versionConflict()
	}
	return nil
}

func replaceItems(tx *gorm.DB, order Order, rows []Item, now time.Time) error {
	sort.Slice(rows, func(i, j int) bool { return rows[i].ExternalItemID < rows[j].ExternalItemID })
	keys := make([]string, len(rows))
	for i := range rows {
		rows[i].OrderID, rows[i].UpdatedAt = order.ID, now
		keys[i] = rows[i].ExternalItemID
	}
	if len(rows) > 0 {
		columns := strings.Split("title,sku_title,image_url,currency,price_cents,qty,discount_cents,amount_cents,order_discount_cents,allocation_source,refunded_amount_cents,refund_status,last_refunded_at,refund_rejection_code,updated_at,deleted_at", ",")
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "tenant_id"}, {Name: "order_id"}, {Name: "external_item_id"}}, DoUpdates: clause.AssignmentColumns(columns)}).CreateInBatches(&rows, 500).Error; err != nil {
			return err
		}
	}
	remove := tx.Model(&Item{}).Where("tenant_id=? AND order_id=? AND deleted_at IS NULL", order.TenantID, order.ID)
	if len(keys) > 0 {
		remove = remove.Where("external_item_id NOT IN ?", keys)
	}
	return remove.Updates(map[string]any{"deleted_at": now, "updated_at": now}).Error
}

func (u *Upserter) writeAddress(tx *gorm.DB, order Order, input AddressInput) error {
	var old Address
	err := tx.Select("id,revision").Where("tenant_id=? AND order_id=?", order.TenantID, order.ID).Take(&old).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	row, err := SealAddress(u.sealer, order.TenantID, order.ID, old.Revision+1, input)
	if err != nil {
		return err
	}
	if old.ID == 0 {
		return tx.Create(&row).Error
	}
	columns := strings.Split("receiver_name_cipher,receiver_name_nonce,phone_cipher,phone_nonce,address_cipher,address_nonce,key_version,revision,updated_at", ",")
	result := tx.Model(&Address{}).Where("tenant_id=? AND order_id=? AND revision=?", order.TenantID, order.ID, old.Revision).Select(columns).Updates(&row)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return versionConflict()
	}
	return nil
}

func validateExistingItems(tx *gorm.DB, old Order, s Snapshot, proved bool) (string, error) {
	if old.ID == 0 || s.Items == nil || proved {
		return "", nil
	}
	var rows []Item
	if err := tx.Select("external_item_id,refunded_amount_cents,refund_status").Where("tenant_id=? AND order_id=?", old.TenantID, old.ID).Find(&rows).Error; err != nil {
		return "", err
	}
	known := make(map[string]Item, len(rows))
	for _, row := range rows {
		known[row.ExternalItemID] = row
	}
	for _, incoming := range s.Items.Rows {
		previous := known[incoming.ExternalItemID]
		if previous.RefundedAmountCents != nil && (incoming.RefundedAmountCents == nil || *incoming.RefundedAmountCents < *previous.RefundedAmountCents) {
			return "rollback_requires_evidence", nil
		}
		if previous.RefundStatus != "" && previous.RefundStatus != "unknown" && !reachable(stateGraphs["after_sales"], previous.RefundStatus, incoming.RefundStatus, map[string]bool{}) {
			return "rollback_requires_evidence", nil
		}
	}
	return "", nil
}
