package order

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/catalog"
)

// ExportProjection contains only business fields approved for ordinary order CSVs.
type ExportProjection struct {
	ID                  int64      `json:"id"`
	ShopID              int64      `json:"shop_id"`
	OrderNo             string     `json:"order_no"`
	Status              string     `json:"status"`
	Currency            string     `json:"currency"`
	GoodsAmountCents    int64      `json:"goods_amount_cents"`
	ShippingFeeCents    int64      `json:"shipping_fee_cents"`
	DiscountCents       int64      `json:"discount_cents"`
	TotalAmountCents    int64      `json:"total_amount_cents"`
	PaidAmountCents     int64      `json:"paid_amount_cents"`
	RefundedAmountCents int64      `json:"refunded_amount_cents"`
	ItemCount           int        `json:"item_count"`
	PlacedAt            *time.Time `json:"placed_at"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

// ExportCSVHeader returns an owned, fixed whitelist, independent from stored payload keys.
func ExportCSVHeader() []string {
	return []string{"id", "shop_id", "order_no", "status", "currency", "goods_amount_cents", "shipping_fee_cents", "discount_cents", "total_amount_cents", "paid_amount_cents", "refunded_amount_cents", "item_count", "placed_at", "created_at", "updated_at"}
}

// ExportCSVRecord rejects projections outside the whitelist and safely encodes every cell.
func ExportCSVRecord(raw json.RawMessage) ([]string, error) {
	var row ExportProjection
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&row) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return nil, errors.New("invalid order export projection")
	}
	placed := ""
	if row.PlacedAt != nil {
		placed = row.PlacedAt.UTC().Format(time.RFC3339Nano)
	}
	cells := []string{strconv.FormatInt(row.ID, 10), strconv.FormatInt(row.ShopID, 10), row.OrderNo, row.Status, row.Currency,
		strconv.FormatInt(row.GoodsAmountCents, 10), strconv.FormatInt(row.ShippingFeeCents, 10), strconv.FormatInt(row.DiscountCents, 10),
		strconv.FormatInt(row.TotalAmountCents, 10), strconv.FormatInt(row.PaidAmountCents, 10), strconv.FormatInt(row.RefundedAmountCents, 10),
		strconv.Itoa(row.ItemCount), placed, row.CreatedAt.UTC().Format(time.RFC3339Nano), row.UpdatedAt.UTC().Format(time.RFC3339Nano)}
	for i := range cells {
		cells[i] = catalog.EscapeExportCell(cells[i])
	}
	return cells, nil
}
