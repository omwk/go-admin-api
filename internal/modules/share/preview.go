package share

import (
	"context"

	"github.com/omwk/go-admin-api/internal/modules/share/pricing"
)

// Preview evaluates all requested members against one consistent database snapshot.
func (s *Service) Preview(ctx context.Context, a Actor, in PreviewInput) ([]PreviewResult, error) {
	if in.CustomerID <= 0 || in.PriceListID < 0 || !validCurrency(in.Currency) || !utcTime(in.AsOf) || len(in.Items) == 0 || len(in.Items) > 500 {
		return nil, Invalid()
	}
	asOf := s.now().UTC()
	if in.AsOf != nil {
		asOf = in.AsOf.UTC()
	}
	out := make([]PreviewResult, 0, len(in.Items))
	err := s.store.Snapshot(ctx, func(tx context.Context) error {
		customer, err := s.store.Customer(tx, a.TenantID, in.CustomerID, false)
		if err != nil {
			return err
		}
		if in.PriceListID > 0 {
			list, err := s.store.PriceList(tx, a.TenantID, in.PriceListID, false)
			if err != nil {
				return err
			}
			if list.Type != "customer" || list.CustomerID == nil || *list.CustomerID != customer.ID || list.Currency != in.Currency {
				return Invalid()
			}
		}
		ids := make([]int64, 0, len(in.Items))
		for _, item := range in.Items {
			if item.SPUID <= 0 || item.SKUID < 0 || item.Quantity < 0 {
				return Invalid()
			}
			ids = append(ids, item.SPUID)
		}
		candidates, err := s.store.Candidates(tx, a.TenantID, in.CustomerID, ids)
		if err != nil {
			return err
		}
		for _, item := range in.Items {
			product, err := s.store.Product(tx, a.TenantID, item.SPUID, item.SKUID)
			if err != nil {
				return err
			}
			original := product.PriceCents
			if product.Currency != in.Currency {
				original = nil
			}
			quantity := item.Quantity
			if quantity == 0 {
				quantity = 1
			}
			result, err := pricing.Resolve(pricing.Input{TenantID: a.TenantID, CustomerID: customer.ID, CustomerActive: customer.Status == "active", PriceListID: in.PriceListID,
				SPUID: item.SPUID, SKUID: item.SKUID, Currency: in.Currency, AsOf: asOf, Quantity: quantity, AllowFallback: in.AllowFallback, AllowZero: in.AllowZero,
				OriginalPriceCents: original, Candidates: candidates})
			if err != nil {
				return err
			}
			out = append(out, PreviewResult{SPUID: item.SPUID, SKUID: item.SKUID, Result: result})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
