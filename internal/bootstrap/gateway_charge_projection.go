package bootstrap

import (
	"context"

	"github.com/omwk/go-admin-api/internal/modules/gateway"
)

func (s *gatewayStore) projectCallCharge(ctx context.Context, c gateway.Call, values map[string]any) error {
	values["charge_source"], values["entitlement_id"], values["cost_cents"], values["cost_units"] = "none", int64(0), int64(0), int64(0)
	if c.BillingMode != "per_call" || !c.Admitted {
		return nil
	}
	charge, err := (&billingStore{database: s.database}).Charge(ctx, c.TenantID, c.ID)
	if err != nil {
		return err
	}
	values["charge_source"], values["entitlement_id"] = charge.Source, charge.EntitlementID
	if c.State == "succeeded" {
		if charge.Source == "wallet" {
			values["cost_cents"] = charge.PreCents
		} else {
			values["cost_units"] = charge.ReservedUnits
		}
	}
	return nil
}
