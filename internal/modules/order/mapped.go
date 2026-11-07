package order

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/omwk/go-admin-api/internal/modules/ingestion"
	"github.com/shopspring/decimal"
	"strings"
	"time"
)

type canonicalHeader struct {
	ExternalID          string     `json:"external_order_id"`
	OrderNo             string     `json:"order_no"`
	Lifecycle           string     `json:"lifecycle_status"`
	Fulfillment         string     `json:"fulfillment_status"`
	Payment             string     `json:"payment_status"`
	AfterSales          string     `json:"after_sales_status"`
	Cancel              string     `json:"cancel_status"`
	PlatformStatus      string     `json:"platform_status"`
	Currency            string     `json:"currency"`
	Goods               *int64     `json:"goods_amount_cents"`
	Shipping            *int64     `json:"shipping_fee_cents"`
	Discount            *int64     `json:"discount_cents"`
	Total               *int64     `json:"total_amount_cents"`
	Paid                *int64     `json:"paid_amount_cents"`
	Refunded            *int64     `json:"refunded_amount_cents"`
	Settled             *bool      `json:"payment_settled"`
	Revision            *int64     `json:"revision"`
	PlacedAt            *time.Time `json:"placed_at"`
	OccurredAt          *time.Time `json:"occurred_at"`
	LastRefundedAt      *time.Time `json:"last_refunded_at"`
	RefundRejectionCode string     `json:"refund_rejection_code"`
	Complete            bool       `json:"items_complete"`
	HasMore             bool       `json:"items_has_more"`
	ExpectedCount       *int       `json:"items_expected_count"`
	EmptyConfirmed      bool       `json:"items_empty_confirmed"`
	AllocationSource    string     `json:"allocation_source"`
	ReceiverName        *string    `json:"receiver_name"`
	Phone               *string    `json:"phone"`
	Address             *string    `json:"address"`
	Deleted             bool       `json:"deleted"`
	VanishReason        string     `json:"vanish_reason"`
}

type canonicalItem struct {
	ExternalID          string     `json:"external_item_id"`
	Title               string     `json:"title"`
	SKUTitle            string     `json:"sku_title"`
	ImageURL            string     `json:"image_url"`
	Currency            string     `json:"currency"`
	Price               *int64     `json:"price_cents"`
	Qty                 *int64     `json:"qty"`
	Discount            *int64     `json:"discount_cents"`
	Amount              *int64     `json:"amount_cents"`
	Eligible            bool       `json:"discount_eligible"`
	OrderDiscount       *int64     `json:"order_discount_cents"`
	Refunded            *int64     `json:"refunded_amount_cents"`
	RefundStatus        string     `json:"refund_status"`
	LastRefundedAt      *time.Time `json:"last_refunded_at"`
	RefundRejectionCode string     `json:"refund_rejection_code"`
}

func decodeCanonical(values map[string]any, out any, item bool) error {
	canonical := make(map[string]any, len(values))
	for key, value := range values {
		if !ingestion.OrderTargetAllowed(key, item) {
			return errors.New("invalid_value")
		}
		if value != nil && (strings.HasSuffix(key, "_cents") || key == "qty" || key == "revision" || key == "items_expected_count") {
			n, ok := orderInteger(value)
			if !ok {
				return errors.New("type_mismatch")
			}
			value = n
		}
		canonical[key] = value
	}
	raw, err := json.Marshal(canonical)
	if err != nil {
		return errors.New("type_mismatch")
	}
	defer clear(raw)
	if json.Unmarshal(raw, out) != nil {
		return errors.New("type_mismatch")
	}
	return nil
}

func orderInteger(value any) (int64, bool) {
	switch value.(type) {
	case int, int32, int64, json.Number, decimal.Decimal:
	default:
		return 0, false
	}
	n, err := decimal.NewFromString(fmt.Sprint(value))
	if err != nil || !n.IsInteger() || n.LessThan(decimal.NewFromInt(-1<<63)) || n.GreaterThan(decimal.NewFromInt(1<<63-1)) {
		return 0, false
	}
	return n.IntPart(), true
}

func mappedSnapshot(m ingestion.Mapped) (Snapshot, canonicalHeader, error) {
	var h canonicalHeader
	if err := decodeCanonical(m.Values, &h, false); err != nil {
		return Snapshot{}, h, err
	}
	s := Snapshot{TenantID: m.TenantID, ShopID: m.ShopID, PlatformID: m.PlatformID, ExternalOrderID: m.ExternalID, StableIdentity: true,
		Source: Source{RawID: m.RawID, FetchedAt: m.SourceFetchedAt, MappingChecksum: m.MappingChecksum}, Mode: ChangeSnapshot, Revision: h.Revision}
	if h.ExternalID == "" || h.ExternalID != m.ExternalID {
		return s, h, errors.New("invalid_value")
	}
	if h.Deleted {
		if h.VanishReason != "platform_deleted" && h.VanishReason != "platform_not_found" {
			return s, h, errors.New("invalid_value")
		}
		return s, h, nil
	}
	if h.Goods == nil || h.Shipping == nil || h.Discount == nil || h.Total == nil || h.Paid == nil || h.Refunded == nil || h.Settled == nil {
		return s, h, errors.New("missing_required")
	}
	s.OrderNo, s.PlatformStatus, s.Currency = h.OrderNo, h.PlatformStatus, h.Currency
	s.States = States{h.Lifecycle, h.Fulfillment, h.Payment, h.AfterSales, h.Cancel}
	decision, err := DecideStates(States{}, s.States, ChangeSnapshot, false)
	if err != nil || decision.Reason != "" {
		return s, h, errors.New("invalid_value")
	}
	s.Money = Money{*h.Goods, *h.Shipping, *h.Discount, *h.Total, *h.Paid, *h.Refunded}
	s.PaymentSettled, s.PlacedAt, s.OccurredAt = *h.Settled, h.PlacedAt, h.OccurredAt
	s.LastRefundedAt, s.RefundRejectionCode = h.LastRefundedAt, h.RefundRejectionCode
	if h.ReceiverName != nil || h.Phone != nil || h.Address != nil {
		if h.ReceiverName == nil || h.Phone == nil || h.Address == nil {
			return s, h, errors.New("missing_required")
		}
		s.Address = &AddressInput{[]byte(*h.ReceiverName), []byte(*h.Phone), []byte(*h.Address)}
	}
	err = decodeMappedItems(m, h, &s)
	return s, h, err
}

func decodeMappedItems(m ingestion.Mapped, h canonicalHeader, s *Snapshot) error {
	for key := range m.Collections {
		if key != "items" {
			return errors.New("invalid_value")
		}
	}
	rows, present := m.Collections["items"]
	if !present {
		return errors.New("missing_required")
	}
	if h.ExpectedCount == nil {
		return errors.New("missing_required")
	}
	if !h.Complete || h.HasMore || *h.ExpectedCount != len(rows) || (len(rows) == 0 && !h.EmptyConfirmed) {
		return errors.New("invalid_value")
	}
	s.Items = &ItemSet{Complete: h.Complete, HasMore: h.HasMore, ExpectedCount: *h.ExpectedCount, EmptyConfirmed: h.EmptyConfirmed, AllocationSource: h.AllocationSource}
	for _, values := range rows {
		var i canonicalItem
		if err := decodeCanonical(values, &i, true); err != nil {
			return err
		}
		if i.Price == nil || i.Qty == nil || i.Discount == nil || i.Amount == nil {
			return errors.New("missing_required")
		}
		s.Items.Rows = append(s.Items.Rows, ItemInput{ExternalItemID: i.ExternalID, Title: i.Title, SKUTitle: i.SKUTitle, ImageURL: i.ImageURL, Currency: i.Currency,
			PriceCents: *i.Price, Qty: *i.Qty, DiscountCents: *i.Discount, AmountCents: *i.Amount, DiscountEligible: i.Eligible, OrderDiscountCents: i.OrderDiscount,
			RefundedAmountCents: i.Refunded, RefundStatus: i.RefundStatus, LastRefundedAt: i.LastRefundedAt, RefundRejectionCode: i.RefundRejectionCode})
	}
	return nil
}
