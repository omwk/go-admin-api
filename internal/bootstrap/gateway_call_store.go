package bootstrap

import (
	"context"
	"math"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/modules/gateway"
	"gorm.io/gorm/clause"
)

const gatewayCallColumns = "call_id,tenant_id,client_id,route_id,route_version,trace_id,request_id,method,path,ip_hash,called_at,deadline_at,state,admitted,status_code,error_code,attempts,result,result_hash,metered_units,finished_at,app_id,billing_mode,currency,price_cents,charge_units,idempotency_key,request_hash,charge_source,entitlement_id,cost_cents,cost_units"

func (s *gatewayStore) InsertCall(ctx context.Context, call gateway.Call) (bool, error) {
	if !db.InTransaction(ctx) {
		return false, gateway.Unavailable()
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	result := q.Table("api_call").Clauses(clause.OnConflict{DoNothing: true}).Create(&call)
	if result.Error != nil || result.RowsAffected != 1 {
		return false, gatewayDBError(result.Error)
	}
	log := map[string]any{"call_id": call.ID, "tenant_id": call.TenantID, "client_id": call.ClientID, "route_id": call.RouteID,
		"route_version": call.RouteVersion, "trace_id": call.TraceID, "request_id": call.RequestID, "method": call.Method,
		"path": call.Path, "ip_hash": call.IPHash, "called_at": call.CalledAt, "state": "processing", "admitted": call.Admitted,
		"status_code": 0, "latency_ms": 0, "req_bytes": 0, "resp_bytes": 0, "billing_mode": call.BillingMode}
	return true, gatewayDBError(q.Table("api_call_log").Create(log).Error)
}

func (s *gatewayStore) Call(ctx context.Context, id string) (gateway.Call, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	if db.InTransaction(ctx) {
		q = q.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	var call gateway.Call
	err := q.Table("api_call").Select(gatewayCallColumns).Where("call_id=?", id).Take(&call).Error
	return call, gatewayDBError(err)
}

func (s *gatewayStore) RequestCall(ctx context.Context, wanted gateway.Call) (gateway.Call, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	if db.InTransaction(ctx) {
		q = q.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	var call gateway.Call
	err := q.Table("api_call").Select(gatewayCallColumns).Where("tenant_id=? AND client_id=? AND idempotency_key=?", wanted.TenantID, wanted.ClientID, wanted.IdempotencyKey).Take(&call).Error
	return call, gatewayDBError(err)
}

func (s *gatewayStore) SaveCallResult(ctx context.Context, call gateway.Call) error {
	if !db.InTransaction(ctx) || call.FinishedAt == nil {
		return gateway.Unavailable()
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	values := map[string]any{"state": call.State, "status_code": call.StatusCode, "error_code": call.ErrorCode,
		"attempts": call.Attempts, "result_hash": call.ResultHash, "metered_units": call.MeteredUnits, "finished_at": call.FinishedAt}
	if err := s.projectCallCharge(ctx, call, values); err != nil {
		return err
	}
	if err := gatewayMutationError(q.Table("api_call").Where("call_id=? AND state='processing'", call.ID).
		Updates(mapWithGatewayResult(values, string(call.Result)))); err != nil {
		return err
	}
	latency := call.FinishedAt.Sub(call.CalledAt).Milliseconds()
	latency = max(0, min(latency, int64(math.MaxInt32)))
	values["latency_ms"], values["resp_bytes"] = latency, len(call.Result)
	values["billed"] = call.State == "succeeded" && values["charge_source"] == "wallet"
	if call.State == "failed" {
		values["resp_bytes"] = 0
	}
	return gatewayMutationError(q.Table("api_call_log").Where("call_id=? AND called_at=? AND state='processing'", call.ID, call.CalledAt).Updates(values))
}

func mapWithGatewayResult(values map[string]any, result string) map[string]any {
	out := make(map[string]any, len(values)+1)
	for key, value := range values {
		out[key] = value
	}
	out["result"] = result
	return out
}

func (s *gatewayStore) InsertMeter(ctx context.Context, call gateway.Call) error {
	if !db.InTransaction(ctx) || call.State != "succeeded" || call.MeteredUnits != 1 {
		return gateway.Unavailable()
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	return gatewayDBError(q.Table("api_call_meter").Create(map[string]any{"call_id": call.ID, "tenant_id": call.TenantID,
		"client_id": call.ClientID, "route_id": call.RouteID, "called_at": call.CalledAt, "units": 1}).Error)
}

func (s *gatewayStore) ExpiredCalls(ctx context.Context, now time.Time) ([]gateway.Call, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	calls := []gateway.Call{}
	err := q.Table("api_call").Select(gatewayCallColumns).Where("state='processing' AND deadline_at<=?", now).
		Order("deadline_at,call_id").Limit(500).Find(&calls).Error
	return calls, gatewayDBError(err)
}

func (s *gatewayStore) Usage(ctx context.Context, p gateway.Principal, from, to time.Time) (gateway.Usage, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var usage gateway.Usage
	err := q.Table("api_call_log").Select("count(*) AS calls, count(*) FILTER (WHERE state='succeeded') AS succeeded_calls, count(*) FILTER (WHERE state='failed') AS failed_calls, count(*) FILTER (WHERE state='processing') AS processing_calls, coalesce(sum(metered_units),0) AS metered_calls").
		Where("tenant_id=? AND client_id=? AND called_at>=? AND called_at<?", p.TenantID, p.ClientID, from, to).Scan(&usage).Error
	return usage, gatewayDBError(err)
}

var _ gateway.CallStore = (*gatewayStore)(nil)
