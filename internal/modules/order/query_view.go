package order

import "time"

// View exposes canonical facts only; it never serializes persistence models.
type View struct {
	ID                  int64      `json:"id"`
	ShopID              int64      `json:"shop_id"`
	PlatformID          int64      `json:"platform_id"`
	ExternalOrderID     string     `json:"external_order_id"`
	OrderNo             string     `json:"order_no"`
	Status              string     `json:"status"`
	LifecycleStatus     string     `json:"lifecycle_status"`
	FulfillmentStatus   string     `json:"fulfillment_status"`
	PaymentStatus       string     `json:"payment_status"`
	AfterSalesStatus    string     `json:"after_sales_status"`
	CancelStatus        string     `json:"cancel_status"`
	PlatformStatus      string     `json:"platform_status"`
	Currency            string     `json:"currency"`
	GoodsAmountCents    int64      `json:"goods_amount_cents"`
	ShippingFeeCents    int64      `json:"shipping_fee_cents"`
	DiscountCents       int64      `json:"discount_cents"`
	TotalAmountCents    int64      `json:"total_amount_cents"`
	PaidAmountCents     int64      `json:"paid_amount_cents"`
	RefundedAmountCents int64      `json:"refunded_amount_cents"`
	FormulaDeltaCents   int64      `json:"formula_delta_cents"`
	PaymentDeltaCents   int64      `json:"payment_delta_cents"`
	ItemCount           int        `json:"item_count"`
	PlacedAt            *time.Time `json:"placed_at"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
	VanishedAt          *time.Time `json:"vanished_at"`
	VanishReason        *string    `json:"vanish_reason"`
}

// Page follows the shared pagination response contract.
type Page struct {
	List     []View `json:"list"`
	Total    int64  `json:"total"`
	Page     int    `json:"page"`
	PageSize int    `json:"page_size"`
}

// ItemView exposes the original sale snapshot without platform extension data.
type ItemView struct {
	ID                  int64  `json:"id"`
	ExternalItemID      string `json:"external_item_id"`
	Title               string `json:"title"`
	SKUTitle            string `json:"sku_title"`
	Currency            string `json:"currency"`
	PriceCents          int64  `json:"price_cents"`
	Qty                 int64  `json:"qty"`
	DiscountCents       int64  `json:"discount_cents"`
	AmountCents         int64  `json:"amount_cents"`
	OrderDiscountCents  int64  `json:"order_discount_cents"`
	AllocationSource    string `json:"allocation_source"`
	RefundedAmountCents *int64 `json:"refunded_amount_cents"`
	RefundStatus        string `json:"refund_status"`
}

// HistoryView includes safe raw references instead of raw response bodies.
type HistoryView struct {
	ID         int64      `json:"id"`
	Dimension  string     `json:"dimension"`
	FromStatus string     `json:"from_status"`
	ToStatus   string     `json:"to_status"`
	EventKind  string     `json:"event_kind"`
	Reason     string     `json:"reason"`
	OccurredAt *time.Time `json:"occurred_at"`
	ObservedAt time.Time  `json:"observed_at"`
	Source     *Source    `json:"source,omitempty" gorm:"-"`
}

// Detail is a non-PII aggregate projection from one consistent database view.
type Detail struct {
	View
	Items   []ItemView    `json:"items"`
	History []HistoryView `json:"history"`
	Raw     Source        `json:"raw"`
}

// Stat groups historical facts by UTC date, compatible status, and currency.
type Stat struct {
	Date                string `json:"date"`
	Status              string `json:"status"`
	Currency            string `json:"currency"`
	OrderCount          int64  `json:"order_count"`
	TotalAmountCents    int64  `json:"total_amount_cents"`
	PaidAmountCents     int64  `json:"paid_amount_cents"`
	RefundedAmountCents int64  `json:"refunded_amount_cents"`
}
