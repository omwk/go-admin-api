package pricing

import (
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/stretchr/testify/require"
)

func sample() Input {
	price := int64(900)
	return Input{TenantID: 1, CustomerID: 11, CustomerActive: true, PriceListID: 21, SPUID: 31, SKUID: 41,
		Currency: "CNY", AsOf: time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC), Quantity: 5,
		AllowFallback: true, OriginalPriceCents: &price, Candidates: []Candidate{
			{TenantID: 1, CustomerID: 11, PriceListID: 21, ItemID: 51, SPUID: 31, Type: "customer", Status: "active", Currency: "CNY", PriceCents: 100, MOQ: 2, Version: 1},
			{TenantID: 1, CustomerID: 11, PriceListID: 22, ItemID: 52, SPUID: 31, SKUID: 41, Type: "customer", Status: "active", Currency: "CNY", PriceCents: 200, MOQ: 1, Version: 2},
			{TenantID: 1, PriceListID: 23, ItemID: 53, SPUID: 31, SKUID: 41, Type: "default", Status: "active", Currency: "CNY", PriceCents: 300, MOQ: 1, Version: 3},
		}}
}

func TestResolvePublicCatalogNeverUsesCustomerPrices(t *testing.T) {
	in := sample()
	in.CustomerID, in.CustomerActive, in.PriceListID = 0, false, 0
	out, err := Resolve(in)
	require.NoError(t, err)
	require.Equal(t, DefaultList, out.PriceBasis)
	require.EqualValues(t, 300, *out.PriceCents)
	in.CustomerActive = true
	_, err = Resolve(in)
	require.Error(t, err)
}

func TestResolveFourLayersAndSpecificity(t *testing.T) {
	// REQ-SHR-002/009: source layer wins before SKU precision, with explicit provenance.
	for _, tc := range []struct {
		name   string
		remove int
		basis  Basis
		cents  int64
	}{
		{"customer SPU beats lower SKU", 0, CustomerQuote, 100},
		{"customer list", 1, CustomerList, 200}, {"default list", 2, DefaultList, 300}, {"original", 3, Original, 900},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := sample()
			in.Candidates = in.Candidates[tc.remove:]
			out, err := Resolve(in)
			require.NoError(t, err)
			require.Equal(t, tc.basis, out.PriceBasis)
			require.Equal(t, tc.cents, *out.PriceCents)
			require.Equal(t, in.AsOf, out.AsOf)
			require.True(t, out.ShowPrice)
			require.NotEmpty(t, out.LayerName)
		})
	}
	in := sample()
	sku := in.Candidates[0]
	sku.ItemID = 54
	sku.SKUID = 41
	sku.PriceCents = 50
	in.Candidates = append(in.Candidates, sku)
	out, err := Resolve(in)
	require.NoError(t, err)
	require.EqualValues(t, 50, *out.PriceCents)
	require.Equal(t, "sku", out.Precision)
}

func TestResolveRejectsSameLayerConflictRegardlessOfPrice(t *testing.T) {
	for _, index := range []int{0, 1, 2} {
		in := sample()
		in.Candidates = in.Candidates[index:]
		duplicate := in.Candidates[0]
		duplicate.ItemID += 10
		duplicate.PriceListID += 10
		if index == 0 {
			duplicate.PriceListID = in.PriceListID
		}
		in.Candidates = append(in.Candidates, duplicate)
		out, err := Resolve(in)
		require.Error(t, err)
		require.Equal(t, errs.ErrConflict, errs.From(err).Code)
		require.Empty(t, out)
		require.Contains(t, errs.From(err).Message, "冲突")
	}
}

func TestResolveIdentityStatusTimeCurrencyAndPolicy(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Input)
		basis  Basis
	}{
		{"other customer", func(in *Input) { in.CustomerID = 12 }, DefaultList},
		{"disabled customer", func(in *Input) { in.CustomerActive = false }, DefaultList},
		{"other tenant", func(in *Input) { in.TenantID = 2 }, Original},
		{"currency", func(in *Input) {
			for i := range in.Candidates {
				in.Candidates[i].Currency = "USD"
			}
		}, Original},
		{"expired", func(in *Input) {
			for i := range in.Candidates {
				in.Candidates[i].ValidTo = &in.AsOf
			}
		}, Original},
		{"not yet valid", func(in *Input) {
			v := in.AsOf.Add(time.Second)
			for i := range in.Candidates {
				in.Candidates[i].ValidFrom = &v
			}
		}, Original},
		{"starts inclusive", func(in *Input) { in.Candidates[0].ValidFrom = &in.AsOf }, CustomerQuote},
		{"disabled list", func(in *Input) { in.Candidates[0].Status = "disabled" }, CustomerList},
		{"unrelated SKU", func(in *Input) { in.Candidates[0].SKUID = 99 }, CustomerList},
		{"fallback off", func(in *Input) { in.Candidates = nil; in.AllowFallback = false }, Unpriced},
		{"no original", func(in *Input) { in.Candidates = nil; in.OriginalPriceCents = nil }, Unpriced},
		{"SPU has no original SKU price", func(in *Input) { in.Candidates = nil; in.SKUID = 0 }, Unpriced},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := sample()
			tc.change(&in)
			out, err := Resolve(in)
			require.NoError(t, err)
			require.Equal(t, tc.basis, out.PriceBasis)
			if out.PriceBasis == Unpriced {
				require.Nil(t, out.PriceCents)
				require.False(t, out.ShowPrice)
			}
		})
	}
}

func TestResolveMOQZeroAndInvalidAmounts(t *testing.T) {
	in := sample()
	in.Quantity = 1
	_, err := Resolve(in)
	require.Error(t, err)
	require.Equal(t, errs.ErrConflict, errs.From(err).Code)
	in = sample()
	in.Candidates[0].PriceCents = 0
	_, err = Resolve(in)
	require.Error(t, err)
	in.AllowZero = true
	out, err := Resolve(in)
	require.NoError(t, err)
	require.Zero(t, *out.PriceCents)
	require.True(t, out.ShowPrice)
	in.Candidates[0].PriceCents = -1
	_, err = Resolve(in)
	require.Error(t, err)
	in = sample()
	in.AsOf = time.Time{}
	_, err = Resolve(in)
	require.Error(t, err)
}
