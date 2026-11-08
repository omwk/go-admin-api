package share

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"slices"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/share/pricing"
)

func (s *LinkService) freeze(ctx context.Context, a Actor, in LinkInput) (Frozen, error) {
	now := s.now().UTC()
	if in.CustomerID > 0 {
		customer, err := s.store.Customer(ctx, a.TenantID, in.CustomerID, false)
		if err != nil {
			return Frozen{}, err
		}
		if customer.Status != "active" {
			return Frozen{}, Invalid()
		}
	}
	if in.PriceListID > 0 {
		list, err := s.store.PriceList(ctx, a.TenantID, in.PriceListID, false)
		if err != nil {
			return Frozen{}, err
		}
		if !publishableList(list, in, now) {
			return Frozen{}, Invalid()
		}
		in.ExpiresAt = earlier(in.ExpiresAt, list.ValidTo)
	}
	products, err := s.store.PublicationProducts(ctx, a.TenantID, in.Items)
	if err != nil {
		return Frozen{}, err
	}
	ids := make([]int64, 0, len(in.Items))
	for _, m := range in.Items {
		ids = append(ids, m.SPUID)
	}
	candidates, err := s.store.Candidates(ctx, a.TenantID, in.CustomerID, ids)
	if err != nil {
		return Frozen{}, err
	}
	if in.CustomerID == 0 && in.PriceListID > 0 {
		candidates = slices.DeleteFunc(candidates, func(c pricing.Candidate) bool { return c.PriceListID != in.PriceListID })
	}
	version, err := generateToken(rand.Reader)
	if err != nil {
		return Frozen{}, err
	}
	out := Frozen{Content: PublicContent{SchemaVersion: 1, GeneratedAt: now, Mode: "snapshot", PriceBasis: "正式报价", AsOf: now,
		ContentVersion: tokenHash(version), Title: in.Title, ValidUntil: in.ExpiresAt, Currency: in.Currency, Items: []PublicItem{}}, Evidence: []pricing.Result{}}
	for _, product := range products {
		var original *int64
		if product.Currency == in.Currency {
			original = &product.OriginalPrice
		}
		result, err := pricing.Resolve(pricing.Input{TenantID: a.TenantID, CustomerID: in.CustomerID, CustomerActive: in.CustomerID > 0, PriceListID: in.PriceListID,
			SPUID: product.SPUID, SKUID: product.SKUID, Currency: in.Currency, AsOf: now, Quantity: 2147483647, AllowFallback: in.AllowFallback, AllowZero: in.AllowZero,
			OriginalPriceCents: original, Candidates: candidates})
		if err != nil {
			return Frozen{}, err
		}
		product.PublicItem.DisplayPriceCents, product.PublicItem.ShowPrice, product.PublicItem.MOQ = result.PriceCents, result.ShowPrice, result.MOQ
		out.Content.Items = append(out.Content.Items, product.PublicItem)
		out.Evidence = append(out.Evidence, result)
		for _, c := range candidates {
			if c.ItemID == result.ItemID && c.PriceListID == result.PriceListID {
				out.Content.ValidUntil = earlier(out.Content.ValidUntil, c.ValidTo)
			}
		}
	}
	in.Password, in.RevokeOld = "", nil
	out.Policy = in
	raw, err := json.Marshal(out)
	if err != nil {
		return Frozen{}, err
	}
	if len(out.Content.Items) == 0 {
		return Frozen{}, errs.New(70301, 400, "分享内容为空")
	}
	if len(raw) > 1<<20 {
		return Frozen{}, Invalid()
	}
	return out, nil
}

func publishableList(list PriceList, in LinkInput, now time.Time) bool {
	if list.Status != "active" || list.Currency != in.Currency || list.ValidFrom != nil && now.Before(*list.ValidFrom) || list.ValidTo != nil && !now.Before(*list.ValidTo) {
		return false
	}
	if in.CustomerID == 0 {
		return list.Type == "default" && list.CustomerID == nil
	}
	return list.Type == "customer" && list.CustomerID != nil && *list.CustomerID == in.CustomerID
}

func earlier(a, b *time.Time) *time.Time {
	if a == nil || b != nil && b.Before(*a) {
		a = b
	}
	if a == nil {
		return nil
	}
	value := a.UTC()
	return &value
}
