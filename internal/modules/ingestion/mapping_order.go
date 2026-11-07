package ingestion

import "fmt"

var orderHeaderTargets = targetSet("external_order_id", "order_no", "lifecycle_status", "fulfillment_status", "payment_status", "after_sales_status", "cancel_status", "platform_status", "currency", "goods_amount_cents", "shipping_fee_cents", "discount_cents", "total_amount_cents", "paid_amount_cents", "refunded_amount_cents", "payment_settled", "last_refunded_at", "refund_rejection_code", "placed_at", "revision", "occurred_at", "items_complete", "items_has_more", "items_expected_count", "items_empty_confirmed", "allocation_source", "receiver_name", "phone", "address", "deleted", "vanish_reason")
var orderItemTargets = targetSet("external_item_id", "title", "sku_title", "image_url", "currency", "price_cents", "qty", "discount_cents", "amount_cents", "discount_eligible", "order_discount_cents", "refunded_amount_cents", "refund_status", "last_refunded_at", "refund_rejection_code")

func targetSet(names ...string) map[string]bool {
	out := make(map[string]bool, len(names))
	for _, name := range names {
		out[name] = true
	}
	return out
}

func validateOrderSpec(spec Spec) error {
	if spec.Resource != string(ResourceOrder) {
		return nil
	}
	for _, field := range spec.Fields {
		if !orderHeaderTargets[field.Target] {
			return fmt.Errorf("unknown order target %q", field.Target)
		}
	}
	for _, collection := range spec.Collections {
		if collection.Target != "items" {
			return fmt.Errorf("unknown order collection %q", collection.Target)
		}
		for _, field := range collection.Fields {
			if !orderItemTargets[field.Target] {
				return fmt.Errorf("unknown order item target %q", field.Target)
			}
		}
	}
	return nil
}

// OrderTargetAllowed reports the closed canonical order mapping boundary.
func OrderTargetAllowed(target string, item bool) bool {
	if item {
		return orderItemTargets[target]
	}
	return orderHeaderTargets[target]
}
