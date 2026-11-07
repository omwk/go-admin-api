package order

import (
	"errors"
	"math/big"
	"sort"
)

// MoneyPolicy defines explicitly configured tolerances in minor currency units.
type MoneyPolicy struct {
	FormulaToleranceCents int64
	PaymentToleranceCents int64
}

// Deltas are derived without altering any platform amount.
type Deltas struct{ Formula, Payment int64 }

// Check detects unsafe amounts and only checks payment imbalance when settled.
func (p MoneyPolicy) Check(m Money, settled bool) (Deltas, string) {
	if m.GoodsAmountCents < 0 || m.ShippingFeeCents < 0 || m.DiscountCents < 0 || m.TotalAmountCents < 0 || m.PaidAmountCents < 0 || m.RefundedAmountCents < 0 || m.RefundedAmountCents > m.PaidAmountCents {
		return Deltas{}, "invalid_amount"
	}
	formula := new(big.Int).Add(big.NewInt(m.GoodsAmountCents), big.NewInt(m.ShippingFeeCents))
	if !formula.IsInt64() {
		return Deltas{}, "amount_overflow"
	}
	formula.Sub(formula, big.NewInt(m.DiscountCents))
	if formula.Sign() < 0 {
		return Deltas{}, "invalid_amount"
	}
	formula.Sub(big.NewInt(m.TotalAmountCents), formula)
	if !formula.IsInt64() {
		return Deltas{}, "amount_overflow"
	}
	d := Deltas{Formula: formula.Int64(), Payment: m.PaidAmountCents - m.TotalAmountCents}
	if outsideTolerance(d.Formula, p.FormulaToleranceCents) {
		return d, "formula_delta"
	}
	if settled && outsideTolerance(d.Payment, p.PaymentToleranceCents) {
		return d, "payment_delta"
	}
	return d, ""
}

func outsideTolerance(value, limit int64) bool { return value > limit || value < -limit }

// DiscountWeight has a stable line identity and a nonnegative eligible base.
type DiscountWeight struct {
	ExternalItemID string
	AmountCents    int64
}

type remainder struct {
	index int
	id    string
	value *big.Int
}

// AllocateDiscount uses exact integer arithmetic and stable identity tie breaking.
// The result has the input order; no line receives more than its eligible base.
func AllocateDiscount(discount int64, rows []DiscountWeight) ([]int64, error) {
	total, err := discountBase(discount, rows)
	if err != nil {
		return nil, err
	}
	result := make([]int64, len(rows))
	if discount == 0 {
		return result, nil
	}
	remainders := make([]remainder, len(rows))
	left := discount
	for i, row := range rows {
		numerator := new(big.Int).Mul(big.NewInt(discount), big.NewInt(row.AmountCents))
		quotient, rest := new(big.Int), new(big.Int)
		quotient.QuoRem(numerator, total, rest)
		result[i] = quotient.Int64()
		left -= result[i]
		remainders[i] = remainder{i, row.ExternalItemID, rest}
	}
	sort.Slice(remainders, func(i, j int) bool {
		if cmp := remainders[i].value.Cmp(remainders[j].value); cmp != 0 {
			return cmp > 0
		}
		return remainders[i].id < remainders[j].id
	})
	for i := int64(0); i < left; i++ {
		result[remainders[i].index]++
	}
	return result, nil
}

func discountBase(discount int64, rows []DiscountWeight) (*big.Int, error) {
	if discount < 0 {
		return nil, errors.New("negative order discount")
	}
	total, seen := new(big.Int), make(map[string]bool, len(rows))
	for _, row := range rows {
		if row.ExternalItemID == "" || seen[row.ExternalItemID] || row.AmountCents < 0 {
			return nil, errors.New("invalid discount base")
		}
		seen[row.ExternalItemID] = true
		total.Add(total, big.NewInt(row.AmountCents))
	}
	if total.Cmp(big.NewInt(discount)) < 0 {
		return nil, errors.New("order discount exceeds eligible base")
	}
	return total, nil
}
