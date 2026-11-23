package gateway

import (
	"context"
	"encoding/json"
	"time"
)

// ObservationScope contains a verified management identity and platform privilege.
type ObservationScope struct {
	Actor
	CrossTenant bool
}

// CallFilter bounds a management query without carrying request or response data.
type CallFilter struct {
	ClientID, RouteID int64
	Status, TraceID   string
	From, To          time.Time
	Page, PageSize    int
	GroupBy           string
}

// CallInfo is an explicit body-free management log projection.
type CallInfo struct {
	ID           int64     `json:"id"`
	CallID       string    `json:"call_id"`
	ClientID     int64     `json:"client_id"`
	ClientName   string    `json:"client_name"`
	RouteID      int64     `json:"route_id"`
	RouteCode    string    `json:"route_code"`
	Method       string    `json:"method"`
	Path         string    `json:"path"`
	StatusCode   int       `json:"status_code"`
	LatencyMS    int64     `json:"latency_ms"`
	BillingMode  string    `json:"billing_mode"`
	ChargeSource string    `json:"charge_source"`
	CostUnits    int64     `json:"cost_units"`
	CostCents    int64     `json:"cost_cents"`
	Currency     string    `json:"currency"`
	ErrorCode    string    `json:"error_code"`
	CalledAt     time.Time `json:"called_at"`
	TraceID      string    `json:"trace_id"`
	State        string    `json:"state"`
	Test         bool      `json:"test"`
}

// CallPage preserves the management pagination envelope.
type CallPage struct {
	List     []CallInfo `json:"list"`
	Total    int64      `json:"total"`
	Page     int        `json:"page"`
	PageSize int        `json:"page_size"`
}

// CallSummaryItem describes one UTC aggregate of completed invocations.
type CallSummaryItem struct {
	Key          string  `json:"key"`
	Calls        int64   `json:"calls"`
	SuccessRate  float64 `json:"success_rate"`
	P95LatencyMS float64 `json:"p95_latency_ms"`
	CostCents    int64   `json:"cost_cents"`
	Currency     string  `json:"currency"`
}

// CallSummary excludes in-flight calls from both totals and groups.
type CallSummary struct {
	TotalCalls int64             `json:"total_calls"`
	Items      []CallSummaryItem `json:"items"`
}

// ObservationStore applies tenant predicates before querying observation data.
type ObservationStore interface {
	ObservationClient(context.Context, ObservationScope, int64) (Client, error)
	ObservationRoute(context.Context, int64, int64) (Route, error)
	ListCalls(context.Context, ObservationScope, CallFilter) (CallPage, error)
	SummarizeCalls(context.Context, ObservationScope, CallFilter) (CallSummary, error)
}

// TrialDispatcher opens the real HMAC stack and returns its resource closer.
type TrialDispatcher func(context.Context) (*OpenHandler, func() error, error)

// TryInput selects a registered route; path resolves parameters in its template.
type TryInput struct {
	RouteID int64             `json:"route_id"`
	Method  string            `json:"method,omitempty"`
	Path    string            `json:"path,omitempty"`
	Query   map[string]string `json:"query,omitempty"`
	Body    json.RawMessage   `json:"body"`
}

// TryBilling explicitly reports that manual trials never charge money or units.
type TryBilling struct {
	Mode   string `json:"mode"`
	Units  int64  `json:"units"`
	Billed bool   `json:"billed"`
}

// TryResult captures a bounded public response, including structured denials.
type TryResult struct {
	StatusCode int        `json:"status_code"`
	ErrorCode  int        `json:"error_code"`
	LatencyMS  int64      `json:"latency_ms"`
	Response   any        `json:"response"`
	Truncated  bool       `json:"truncated"`
	Test       bool       `json:"test"`
	CallID     string     `json:"call_id"`
	Billing    TryBilling `json:"billing"`
}
