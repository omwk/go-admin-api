package order

import (
	"errors"
	"math/big"
	"strings"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
)

func validateIdentity(s Snapshot) error {
	if s.TenantID <= 0 || s.ShopID <= 0 || s.PlatformID <= 0 || strings.TrimSpace(s.ExternalOrderID) == "" || s.ExpectedVersion < 0 {
		return errs.Invalid(errors.New("incomplete order identity"))
	}
	if s.Source.RawID <= 0 || s.Source.FetchedAt.IsZero() || s.Source.MappingChecksum == "" || (s.Revision != nil && *s.Revision < 0) {
		return errs.Invalid(errors.New("incomplete order source"))
	}
	switch s.Mode {
	case "", ChangeSnapshot, ChangeCommand, ChangeCorrection:
		return nil
	default:
		return errs.Invalid(errors.New("invalid order change mode"))
	}
}

func (u *Upserter) prepare(old Order, s Snapshot) ([]Item, Deltas, string) {
	deltas, reason := u.policy.Check(s.Money, s.PaymentSettled)
	if reason != "" {
		return nil, deltas, reason
	}
	if !s.StableIdentity {
		return nil, deltas, "unstable_order_identity"
	}
	if !validCurrency(s.Currency) {
		return nil, deltas, "invalid_currency"
	}
	if old.ID != 0 && old.Currency != s.Currency {
		return nil, deltas, "currency_changed"
	}
	rows, reason := prepareItems(s)
	return rows, deltas, reason
}

func validCurrency(currency string) bool {
	if len(currency) != 3 {
		return false
	}
	for _, letter := range currency {
		if letter < 'A' || letter > 'Z' {
			return false
		}
	}
	return true
}

func prepareItems(s Snapshot) ([]Item, string) {
	set := s.Items
	if set == nil {
		return nil, ""
	}
	if !set.Complete || set.HasMore || set.ExpectedCount != len(set.Rows) || (len(set.Rows) == 0 && !set.EmptyConfirmed) {
		return nil, "incomplete_items"
	}
	rows, weights := make([]Item, len(set.Rows)), make([]DiscountWeight, len(set.Rows))
	seen, total := make(map[string]bool, len(set.Rows)), new(big.Int)
	for i, input := range set.Rows {
		if strings.TrimSpace(input.ExternalItemID) == "" || seen[input.ExternalItemID] {
			return nil, "invalid_item_identity"
		}
		seen[input.ExternalItemID] = true
		if reason := validateItem(input, s.Currency); reason != "" {
			return nil, reason
		}
		total.Add(total, big.NewInt(input.AmountCents))
		rows[i] = Item{TenantID: s.TenantID, ExternalItemID: input.ExternalItemID, Title: input.Title, SKUTitle: input.SKUTitle, ImageURL: input.ImageURL,
			Currency: input.Currency, PriceCents: input.PriceCents, Qty: input.Qty, DiscountCents: input.DiscountCents, AmountCents: input.AmountCents,
			RefundedAmountCents: input.RefundedAmountCents, RefundStatus: input.RefundStatus, LastRefundedAt: input.LastRefundedAt, RefundRejectionCode: input.RefundRejectionCode}
		weights[i] = DiscountWeight{ExternalItemID: input.ExternalItemID}
		if input.DiscountEligible {
			weights[i].AmountCents = input.AmountCents
		}
	}
	if !total.IsInt64() || total.Int64() != s.Money.GoodsAmountCents {
		return nil, "item_amount_mismatch"
	}
	return allocateItems(s, rows, weights)
}

func validateItem(input ItemInput, currency string) string {
	if input.Currency != currency {
		return "item_currency_mismatch"
	}
	if input.PriceCents < 0 || input.Qty <= 0 || input.DiscountCents < 0 || input.AmountCents < 0 {
		return "invalid_item_amount"
	}
	amount := new(big.Int).Mul(big.NewInt(input.PriceCents), big.NewInt(input.Qty))
	if !amount.IsInt64() {
		return "amount_overflow"
	}
	amount.Sub(amount, big.NewInt(input.DiscountCents))
	if amount.Sign() < 0 || amount.Int64() != input.AmountCents {
		return "invalid_item_amount"
	}
	if _, exists := stateGraphs["after_sales"][input.RefundStatus]; !exists {
		return "invalid_item_refund"
	}
	if input.RefundedAmountCents != nil && (*input.RefundedAmountCents < 0 || *input.RefundedAmountCents > input.AmountCents) {
		return "invalid_item_refund"
	}
	return ""
}

func allocateItems(s Snapshot, rows []Item, weights []DiscountWeight) ([]Item, string) {
	allocation := s.Items.AllocationSource
	allPlatform := len(rows) > 0
	anyPlatform := false
	for _, input := range s.Items.Rows {
		allPlatform = allPlatform && input.OrderDiscountCents != nil
		anyPlatform = anyPlatform || input.OrderDiscountCents != nil
	}
	if anyPlatform && !allPlatform {
		return nil, "invalid_discount_allocation"
	}
	if allPlatform {
		allocation = "platform"
	}
	if allocation == "platform" {
		total := new(big.Int)
		for i, input := range s.Items.Rows {
			if input.OrderDiscountCents == nil || *input.OrderDiscountCents < 0 || *input.OrderDiscountCents > input.AmountCents {
				return nil, "invalid_discount_allocation"
			}
			rows[i].OrderDiscountCents = *input.OrderDiscountCents
			total.Add(total, big.NewInt(*input.OrderDiscountCents))
		}
		if total.Cmp(big.NewInt(s.Money.DiscountCents)) != 0 {
			return nil, "invalid_discount_allocation"
		}
	} else if allocation == "largest_remainder_v1" {
		allocated, err := AllocateDiscount(s.Money.DiscountCents, weights)
		if err != nil {
			return nil, "invalid_discount_allocation"
		}
		for i := range rows {
			rows[i].OrderDiscountCents = allocated[i]
		}
	} else if allocation != "unknown" {
		return nil, "invalid_discount_allocation"
	}
	for i := range rows {
		rows[i].AllocationSource = allocation
	}
	return rows, ""
}

func correctionProved(old Order, s Snapshot) bool {
	proof := s.Correction
	if s.Mode != ChangeCorrection || proof == nil || proof.ApprovedBy <= 0 || !proof.AuthoritativeDetail || !proof.PlatformSupportsCorrection || old.Revision == nil || s.Revision == nil || *s.Revision <= *old.Revision {
		return false
	}
	switch proof.ReasonCode {
	case "payment_reversal", "refund_reversal", "fulfillment_correction", "lifecycle_reopen", "mapping_correction":
		return true
	default:
		return false
	}
}
