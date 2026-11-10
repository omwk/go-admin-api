package bootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/billing"
	"github.com/omwk/go-admin-api/internal/modules/gateway"
)

func (s *gatewayStore) AdmitBatch(ctx context.Context, c gateway.Call, charges gateway.Charges, at time.Time) (bool, bool, error) {
	if c.BillingMode == "per_call" && c.Admitted {
		port, ok := charges.(*gatewayBilling)
		if !ok || port.database != s.database {
			return false, false, nil
		}
	}
	if !db.InTransaction(ctx) {
		return false, true, gateway.Unavailable()
	}
	raw, err := admissionDocument(c, at)
	if err != nil {
		return false, true, gateway.Unavailable()
	}
	hashRaw, err := json.Marshal([]any{c.ID, c.PriceCents * c.ChargeUnits})
	if err != nil {
		return false, true, gateway.Unavailable()
	}
	hash := sha256.Sum256(hashRaw)
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var answer []byte
	if err := q.Raw("SELECT gateway_admit_call(?::jsonb,?)", string(raw), hex.EncodeToString(hash[:])).Row().Scan(&answer); err != nil {
		return false, true, gatewayDBError(err)
	}
	var out struct {
		Inserted bool      `json:"inserted"`
		Code     int       `json:"code"`
		Kind     string    `json:"kind"`
		Limit    int64     `json:"limit"`
		ResetAt  time.Time `json:"reset_at"`
	}
	if json.Unmarshal(answer, &out) != nil {
		return false, true, gateway.Unavailable()
	}
	if out.Code == 10005 {
		return out.Inserted, true, gateway.QuotaExceeded(out.Kind, c.CalledAt, out.ResetAt, out.Limit)
	}
	return out.Inserted, true, admissionError(out.Code)
}

func admissionDocument(c gateway.Call, at time.Time) ([]byte, error) {
	return json.Marshal(map[string]any{
		"call_id": c.ID, "tenant_id": c.TenantID, "client_id": c.ClientID, "route_id": c.RouteID, "route_version": c.RouteVersion,
		"trace_id": c.TraceID, "request_id": c.RequestID, "method": c.Method, "path": c.Path, "ip_hash": c.IPHash,
		"called_at": c.CalledAt, "deadline_at": c.DeadlineAt, "state": c.State, "admitted": c.Admitted,
		"status_code": c.StatusCode, "error_code": c.ErrorCode, "attempts": c.Attempts, "result": c.Result,
		"result_hash": c.ResultHash, "metered_units": c.MeteredUnits, "finished_at": c.FinishedAt,
		"app_id": c.AppID, "billing_mode": c.BillingMode, "currency": c.Currency, "price_cents": c.PriceCents, "charge_units": c.ChargeUnits,
		"idempotency_key": c.IdempotencyKey, "request_hash": c.RequestHash, "charge_source": c.ChargeSource,
		"entitlement_id": c.EntitlementID, "cost_cents": c.CostCents, "cost_units": c.CostUnits, "reserve_at": at,
	})
}

func admissionError(code int) error {
	switch code {
	case 0:
		return nil
	case 10001:
		return billing.Invalid()
	case 10006:
		return billing.Mismatch()
	case 80104:
		return errs.New(80104, 403, "未获授权访问此接口")
	case 90101:
		return errs.New(90101, 404, "钱包不存在")
	case 90102:
		return billing.Insufficient()
	case 90103:
		return billing.Inactive()
	case 90104:
		return billing.FrozenInsufficient()
	default:
		return gateway.Unavailable()
	}
}
