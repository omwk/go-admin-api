package billing

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestReconcileAnyIntegerDifference(t *testing.T) {
	// REQ-BILL-006: no tolerance for one cent, one record or one unit.
	cash := CallFact{CallID: "cash", State: "succeeded", BillingMode: "per_call", Source: "wallet", ChargeState: "settled", Admitted: true, CallCount: 1, LogCount: 1, MeterCount: 1, MeterUnits: 1, ChargeCount: 1, PriceCents: 5, Units: 1, PreCents: 5, ActualCents: 5, CashCount: 1, CashCents: 5, FreezeCount: 1, FreezeCents: 5, UnfrozenCents: 5, LogCents: 5, DeadlineAt: time.Now()}
	require.Empty(t, CheckCall(cash, 0))
	for _, test := range []struct {
		name   string
		change func(*CallFact)
		kind   string
	}{
		{"cent", func(f *CallFact) { f.CashCents-- }, "cash_cents"},
		{"charge", func(f *CallFact) { f.ChargeCount = 0 }, "charge_count"},
		{"meter", func(f *CallFact) { f.MeterCount = 0 }, "meter_count"},
		{"duplicate", func(f *CallFact) { f.CashCount = 2 }, "cash_count"},
		{"identity", func(f *CallFact) { f.InvalidRefs = 1 }, "attribution"},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := cash
			test.change(&f)
			found := false
			for _, d := range CheckCall(f, 0) {
				if d.Kind == test.kind {
					found = true
				}
			}
			require.True(t, found)
		})
	}
	units := cash
	units.Source = "free"
	units.CashCount, units.CashCents, units.FreezeCount, units.FreezeCents, units.UnfrozenCents, units.LogCents, units.ActualCents = 0, 0, 0, 0, 0, 0, 0
	units.ReservedUnits, units.ConsumeCount, units.ConsumedUnits, units.UnitReserveCount, units.UnitReserved, units.LogUnits = 2, 1, 2, 1, 2, 2
	require.Empty(t, CheckCall(units, 0))
	units.ConsumedUnits--
	require.Equal(t, "consumed_units", CheckCall(units, 0)[0].Kind)
}
