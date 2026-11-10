package gateway

import (
	"context"
	"encoding/json"
	"time"
)

// Call is the server-owned identity and durable result of one logical invocation.
type Call struct {
	ID                                  string `gorm:"column:call_id;primaryKey"`
	TenantID, ClientID, RouteID         int64
	RouteVersion                        int
	AppID, BillingMode, Currency        string
	PriceCents, ChargeUnits             int64
	ChargeSource                        string
	EntitlementID, CostCents, CostUnits int64
	IdempotencyKey, RequestHash         string
	TraceID, RequestID                  string
	Method, Path, IPHash                string
	CalledAt, DeadlineAt                time.Time
	State                               string
	Admitted                            bool
	StatusCode, Attempts                int
	ErrorCode                           string
	Result                              json.RawMessage `gorm:"type:jsonb"`
	ResultHash                          string
	MeteredUnits                        int
	FinishedAt                          *time.Time
}

// CallResult contains only a bounded public result or a sanitized failure code.
type CallResult struct {
	StatusCode, Attempts int
	ErrorCode            string
	Data                 json.RawMessage
}

// CallPolicy bounds execution and keys private observation digests.
type CallPolicy struct {
	Timeout     time.Duration
	MaxAttempts int
	HashKey     []byte
}

// CallStore persists intents, quotas, results and meter events in service transactions.
type CallStore interface {
	Transaction(context.Context, func(context.Context) error) error
	InsertCall(context.Context, Call) (bool, error)
	Call(context.Context, string) (Call, error)
	RequestCall(context.Context, Call) (Call, error)
	ConsumeQuota(context.Context, Call) error
	SaveCallResult(context.Context, Call) error
	InsertMeter(context.Context, Call) error
	ExpiredCalls(context.Context, time.Time) ([]Call, error)
	Usage(context.Context, Principal, time.Time, time.Time) (Usage, error)
}

// Usage is one application's UTC daily aggregation from persisted call logs.
type Usage struct {
	Date            string    `json:"date"`
	Calls           int64     `json:"calls"`
	SucceededCalls  int64     `json:"succeeded_calls"`
	FailedCalls     int64     `json:"failed_calls"`
	ProcessingCalls int64     `json:"processing_calls"`
	MeteredCalls    int64     `json:"metered_calls"`
	AsOf            time.Time `json:"as_of"`
}

type callContextKey struct{}

// CallID returns the stable server identity shared by all internal attempts.
func CallID(ctx context.Context) string {
	id, _ := ctx.Value(callContextKey{}).(string)
	return id
}
