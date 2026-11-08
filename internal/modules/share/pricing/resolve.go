package pricing

import (
	"github.com/omwk/go-admin-api/internal/kernel/errs"
)

// Resolve applies source precedence, then SKU precision, without IO or a clock.
func Resolve(in Input) (Result, error) {
	if in.TenantID <= 0 || in.CustomerID <= 0 || in.SPUID <= 0 || in.SKUID < 0 || in.PriceListID < 0 || len(in.Currency) != 3 || in.AsOf.IsZero() || in.Quantity <= 0 {
		return Result{}, errs.New(errs.ErrInvalidParam, 400, "价格解析参数无效")
	}
	for _, layer := range []Basis{CustomerQuote, CustomerList, DefaultList} {
		var specific, generic []Candidate
		for _, c := range in.Candidates {
			if !eligible(in, c, layer) {
				continue
			}
			if c.SKUID == in.SKUID && in.SKUID > 0 {
				specific = append(specific, c)
			} else if c.SKUID == 0 {
				generic = append(generic, c)
			}
		}
		matches, precision := generic, "spu"
		if len(specific) > 0 {
			matches, precision = specific, "sku"
		}
		if len(matches) > 1 {
			return Result{}, conflict("同层价格冲突", map[string]any{"price_basis": layer, "precision": precision})
		}
		if len(matches) == 1 {
			return resolved(in, matches[0], layer, precision)
		}
	}
	out := Result{Currency: in.Currency, AsOf: in.AsOf.UTC(), PriceBasis: Unpriced, LayerName: "未定价", Reason: "no_eligible_price", MOQ: 1}
	if in.AllowFallback && in.SKUID > 0 && in.OriginalPriceCents != nil {
		price := *in.OriginalPriceCents
		if price < 0 || (price == 0 && !in.AllowZero) {
			return Result{}, errs.New(errs.ErrInvalidParam, 400, "原价无效或未允许零价")
		}
		out.PriceCents = &price
		out.ShowPrice = true
		out.PriceBasis = Original
		out.LayerName = "商品原价"
		out.Precision = "sku"
		out.Reason = "matched"
	}
	return out, nil
}

func eligible(in Input, c Candidate, layer Basis) bool {
	if c.TenantID != in.TenantID || c.SPUID != in.SPUID || c.Status != "active" || c.Currency != in.Currency ||
		(c.ValidFrom != nil && in.AsOf.Before(*c.ValidFrom)) || (c.ValidTo != nil && !in.AsOf.Before(*c.ValidTo)) {
		return false
	}
	switch layer {
	case CustomerQuote:
		return in.CustomerActive && in.PriceListID > 0 && c.Type == "customer" && c.CustomerID == in.CustomerID && c.PriceListID == in.PriceListID
	case CustomerList:
		return in.CustomerActive && c.Type == "customer" && c.CustomerID == in.CustomerID && c.PriceListID != in.PriceListID
	case DefaultList:
		return c.Type == "default" && c.CustomerID == 0
	}
	return false
}

func resolved(in Input, c Candidate, layer Basis, precision string) (Result, error) {
	if c.PriceCents < 0 || c.MOQ <= 0 || (c.PriceCents == 0 && !in.AllowZero) {
		return Result{}, errs.New(errs.ErrInvalidParam, 400, "报价金额或起订量无效")
	}
	if in.Quantity < c.MOQ {
		return Result{}, conflict("未达到命中报价起订量", map[string]any{"moq": c.MOQ, "price_basis": layer})
	}
	name := map[Basis]string{CustomerQuote: "客户专属报价", CustomerList: "客户价目表", DefaultList: "默认价目表"}[layer]
	price := c.PriceCents
	return Result{PriceCents: &price, Currency: c.Currency, ShowPrice: true, PriceBasis: layer, LayerName: name, AsOf: in.AsOf.UTC(), PriceListID: c.PriceListID,
		ItemID: c.ItemID, Version: c.Version, Precision: precision, MOQ: c.MOQ, Reason: "matched"}, nil
}

func conflict(message string, details map[string]any) error {
	err := errs.New(errs.ErrConflict, 409, message)
	err.Details = details
	return err
}
