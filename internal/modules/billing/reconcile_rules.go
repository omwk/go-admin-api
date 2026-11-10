package billing

// CheckCall compares one logical call against all independent financial facts.
func CheckCall(f CallFact, cutoffAt int64) []Difference {
	checks := []Difference{}
	check := func(kind string, want, got int64) {
		if want != got {
			checks = append(checks, Difference{AppID: f.AppID, Currency: f.Currency, CallID: f.CallID, Kind: kind, Expected: want, Actual: got})
		}
	}
	check("call_count", 1, f.CallCount)
	check("log_count", 1, f.LogCount)
	check("attribution", 0, f.InvalidRefs)
	success := f.CallCount == 1 && f.State == "succeeded"
	meter := int64(0)
	if success {
		meter = 1
	}
	check("meter_count", meter, f.MeterCount)
	check("meter_units", meter, f.MeterUnits)
	wantCharge := int64(0)
	if f.CallCount == 1 && f.Admitted && f.BillingMode == "per_call" {
		wantCharge = 1
	}
	check("charge_count", wantCharge, f.ChargeCount)
	if f.State == "processing" && f.DeadlineAt.Unix() <= cutoffAt {
		check("overdue_call", 0, 1)
	}
	if wantCharge == 0 {
		checkUncharged(check, f)
		return checks
	}
	if f.ChargeCount == 0 {
		return checks
	}
	wantState := "reserved"
	if success {
		wantState = "settled"
	} else if f.State == "failed" {
		wantState = "released"
	}
	if f.ChargeState != wantState && !(wantState == "released" && f.ChargeState == "expired") {
		check("charge_state", 0, 1)
	}
	if f.Source == "wallet" {
		checkCash(check, f, success)
	} else if f.Source == "free" || f.Source == "paid" {
		checkUnits(check, f, success)
	} else {
		check("charge_source", 1, 0)
	}
	return checks
}

func checkUncharged(check func(string, int64, int64), f CallFact) {
	check("cash_count", 0, f.CashCount)
	check("consume_count", 0, f.ConsumeCount)
	check("freeze_count", 0, f.FreezeCount)
	check("unit_reserve_count", 0, f.UnitReserveCount)
	check("release_count", 0, f.ReleaseCount+f.UnitReleaseCount)
	check("log_cents", 0, f.LogCents)
	check("log_units", 0, f.LogUnits)
}

func checkCash(check func(string, int64, int64), f CallFact, success bool) {
	wantCount, wantCents, wantUnfreeze, wantRelease := int64(0), int64(0), int64(0), int64(0)
	if success {
		wantCount, wantCents = 1, f.PriceCents*f.Units
	}
	if f.State != "processing" {
		wantUnfreeze = f.PreCents
	}
	if f.State == "failed" {
		wantRelease = 1
	}
	check("pre_cents", f.PriceCents*f.Units, f.PreCents)
	check("freeze_count", 1, f.FreezeCount)
	check("freeze_cents", f.PreCents, f.FreezeCents)
	check("cash_count", wantCount, f.CashCount)
	check("cash_cents", wantCents, f.CashCents)
	check("actual_cents", wantCents, f.ActualCents)
	check("log_cents", wantCents, f.LogCents)
	check("unfrozen_cents", wantUnfreeze, f.UnfrozenCents)
	check("release_count", wantRelease, f.ReleaseCount)
	check("consume_count", 0, f.ConsumeCount)
	check("unit_reserve_count", 0, f.UnitReserveCount)
	check("log_units", 0, f.LogUnits)
}

func checkUnits(check func(string, int64, int64), f CallFact, success bool) {
	wantCount, wantUnits, wantRelease := int64(0), int64(0), int64(0)
	if success {
		wantCount, wantUnits = 1, f.ReservedUnits
	}
	if f.State == "failed" {
		wantRelease = 1
	}
	check("unit_reserve_count", 1, f.UnitReserveCount)
	check("unit_reserved", f.ReservedUnits, f.UnitReserved)
	check("consume_count", wantCount, f.ConsumeCount)
	check("consumed_units", wantUnits, f.ConsumedUnits)
	check("log_units", wantUnits, f.LogUnits)
	check("unit_release_count", wantRelease, f.UnitReleaseCount)
	check("unit_released", wantRelease*f.ReservedUnits, f.UnitReleased)
	check("cash_count", 0, f.CashCount)
	check("actual_cents", 0, f.ActualCents)
	check("cash_cents", 0, f.CashCents)
	check("log_cents", 0, f.LogCents)
	check("freeze_count", 0, f.FreezeCount)
	check("release_count", 0, f.ReleaseCount)
}
