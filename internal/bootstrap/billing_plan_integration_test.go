package bootstrap

import (
	"context"
	"encoding/json"
	"strconv"
	"sync"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/billing"
	"github.com/omwk/go-admin-api/internal/modules/gateway"
	"github.com/stretchr/testify/require"
)

func TestBillingEntitlementIntegrationPurchaseSnapshotAndRollback(t *testing.T) {
	// REQ-BILL-004: purchasing cash and issuing units is one command; consumption has zero cash cost.
	f := newBillingHTTPFixture(t, 100)
	a := billing.Actor{TenantID: 1, UserID: f.gatewayActor().UserID}
	p, err := f.money.SavePlan(f.ctx, a, billing.Plan{Code: "package", Name: "Unit package", Currency: billing.Currency, PriceCents: 20, QuotaType: "units", UnitCount: 3, ValidDays: 30, RouteID: f.route.ID, Status: "active"})
	require.NoError(t, err)
	in := billing.GrantInput{AppID: f.client.AppID, PlanID: p.ID}
	e, err := f.money.Grant(f.ctx, a, in, "purchase")
	require.NoError(t, err)
	duplicate, err := f.money.Grant(f.ctx, a, in, "purchase")
	require.NoError(t, err)
	require.Equal(t, e.ID, duplicate.ID)
	f.assertFunds(t, 80, 0)
	require.Equal(t, e.ID, billingSucceed(t, f).EntitlementID)
	f.assertFunds(t, 80, 0)
	p.PriceCents, p.UnitCount = 30, 5
	p, err = f.money.SavePlan(f.ctx, a, p)
	require.NoError(t, err)
	require.Equal(t, 2, p.Version)
	rows, err := f.money.Entitlements(f.ctx, a, f.client.AppID)
	require.NoError(t, err)
	require.EqualValues(t, 20, rows[0].PurchaseCents)
	require.EqualValues(t, 3, rows[0].TotalUnits)
	require.Equal(t, 1, rows[0].PlanVersion)
	require.Error(t, f.database.Transaction(f.ctx, func(tx context.Context) error {
		_, err := f.money.Grant(tx, a, in, "rollback")
		if err != nil {
			return err
		}
		return billing.Unavailable()
	}))
	f.assertFunds(t, 80, 0)
	in.AutoRenew = true
	_, err = f.money.Grant(f.ctx, a, in, "renew")
	require.Equal(t, 10001, errs.From(err).Code)
	_, err = f.money.Grant(f.ctx, billing.Actor{TenantID: 1, UserID: a.UserID + 100}, billing.GrantInput{AppID: f.client.AppID, PlanID: p.ID}, "foreign")
	require.Error(t, err)
	alerts := []billing.Difference{}
	run := billingRun(t, f, billingReconciler(t, f, &alerts), f.now.Format("2006-01-02"))
	require.Zero(t, run.Differences)
}

func TestBillingEntitlementIntegrationConcurrentReserveAndRelease(t *testing.T) {
	f := newBillingHTTPFixture(t, 100)
	e := billingGift(t, f, "free", 10, nil)
	start := make(chan struct{})
	out := make(chan error, 30)
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		c := f.intent()
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			err := f.calls.Start(f.ctx, c)
			if err == nil {
				err = f.calls.Finish(f.ctx, c.ID, gateway.CallResult{StatusCode: 200, Data: json.RawMessage(`{"ok":true}`)})
			}
			out <- err
		}()
	}
	close(start)
	wg.Wait()
	close(out)
	for err := range out {
		require.NoError(t, err)
	}
	f.assertFunds(t, 0, 0)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var used int64
	require.NoError(t, q.Table("unit_txn").Select("sum(amount)").Where("entitlement_id=? AND type='consume'", e.ID).Scan(&used).Error)
	require.EqualValues(t, 10, used)
	_, err := f.money.Grant(f.ctx, billing.Actor{TenantID: 1, UserID: f.gatewayActor().UserID}, billing.GrantInput{AppID: f.client.AppID, Kind: "free", QuotaType: "calls", Units: 1, SourceRef: "failure grant"}, "failure")
	require.NoError(t, err)
	c := f.intent()
	require.NoError(t, f.calls.Start(f.ctx, c))
	require.NoError(t, f.calls.Finish(f.ctx, c.ID, gateway.CallResult{StatusCode: 502, ErrorCode: "80301"}))
	require.Equal(t, "free", billingSucceed(t, f).Source)
	alerts := []billing.Difference{}
	run := billingRun(t, f, billingReconciler(t, f, &alerts), f.now.Format("2006-01-02"))
	require.Zero(t, run.Differences)
}

func TestBillingHTTPIntegrationBenefitsAndManualReconcile(t *testing.T) {
	f := newBillingHTTPFixture(t, 100)
	w, body := f.manage(t, "POST", "/pricing-plans", map[string]any{"code": "calls", "name": "Call package", "currency": "CNY", "price_cents": 20, "quota_type": "calls", "unit_count": 2, "valid_days": 30, "status": "active"}, "plan")
	require.Equal(t, 201, w.Code, w.Body.String())
	var p billing.Plan
	require.NoError(t, json.Unmarshal(body["data"], &p))
	for i := 0; i < 2; i++ {
		w, _ = f.manage(t, "POST", "/subscriptions", map[string]any{"app_id": f.client.AppID, "plan_id": p.ID}, "subscription")
		require.Equal(t, 201, w.Code, w.Body.String())
	}
	f.assertFunds(t, 80, 0)
	w, body = f.manage(t, "GET", "/entitlements?app_id="+f.client.AppID, nil, "")
	require.Equal(t, 200, w.Code, w.Body.String())
	var rows []billing.Entitlement
	require.NoError(t, json.Unmarshal(body["data"], &rows))
	require.Len(t, rows, 1)
	w, _ = f.manage(t, "GET", "/entitlements/"+strconv.FormatInt(rows[0].ID, 10)+"/txns", nil, "")
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Equal(t, "paid", billingSucceed(t, f).Source)
	var runID int64
	for i := 0; i < 2; i++ {
		w, body = f.manage(t, "POST", "/billing-reconciliations", map[string]any{"service_date": f.now.Format("2006-01-02")}, "run")
		require.Equal(t, 201, w.Code, w.Body.String())
		var run billing.ReconcileRun
		require.NoError(t, json.Unmarshal(body["data"], &run))
		require.Zero(t, run.Differences)
		if i == 0 {
			runID = run.ID
		} else {
			require.Equal(t, runID, run.ID)
		}
	}
	w, _ = f.manage(t, "POST", "/billing-reconciliations", map[string]any{"service_date": f.now.AddDate(0, 0, -1).Format("2006-01-02")}, "run")
	require.Equal(t, 409, w.Code, w.Body.String())
	w, _ = f.manage(t, "GET", "/billing-usage?service_date="+f.now.Format("2006-01-02"), nil, "")
	require.Equal(t, 200, w.Code, w.Body.String())
}
