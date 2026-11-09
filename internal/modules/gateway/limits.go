package gateway

import (
	"context"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
)

// RateDecision describes the most restrictive applicable token bucket.
type RateDecision struct {
	LimitType  string
	Limit      int64
	Remaining  int64
	RetryAfter time.Duration
}

// RateLimiter atomically checks all applicable buckets before decrementing them.
type RateLimiter interface {
	Take(context.Context, Principal, int64) (RateDecision, error)
}

// LimitError carries only the approved quota or rate-limit metadata.
type LimitError struct{ Decision RateDecision }

func (e *LimitError) Error() string { return "gateway admission limit exceeded" }

// Public returns a fresh error with the public details whitelist.
func (e *LimitError) Public() *errs.AppError {
	message := "请求过于频繁"
	if e.Decision.LimitType == "daily_quota" || e.Decision.LimitType == "monthly_quota" {
		message = "调用配额已用尽"
	}
	out := errs.New(10005, 429, message)
	out.Details = map[string]any{"limit_type": e.Decision.LimitType}
	return out
}

// QuotaExceeded reports the UTC period which blocks admission for the longest time.
func QuotaExceeded(kind string, now, reset time.Time, limit int64) error {
	return &LimitError{Decision: RateDecision{LimitType: kind, Limit: limit, RetryAfter: reset.Sub(now)}}
}

// QuotaLimits counts admitted calls independently from success metering.
type QuotaLimits struct {
	Daily   int64 `json:"quota_daily"`
	Monthly int64 `json:"quota_monthly"`
}
