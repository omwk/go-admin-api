// Package refresh implements the durable credential refresh core without a queue.
package refresh

import (
	"crypto/rand"
	"errors"
	"math/big"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/shop/refresh/port"
)

// Durable phases and failure classifications are independent of credential status.
const (
	PhaseIntent      = "intent"
	PhaseSending     = "sending"
	PhaseConsumed    = "consumed"
	PhasePublished   = "published"
	PhaseUnknown     = "unknown"
	FailureTemporary = "temporary"
	FailureInvalid   = "invalid"
	FailureUnknown   = "unknown"
	MaxAttempts      = 5
)

// Config bounds dispatch and local completion and supplies the retry schedule.
type Config struct {
	Backoff       [MaxAttempts]time.Duration
	CallTimeout   time.Duration
	FinishTimeout time.Duration
	Jitter        func(time.Duration) (time.Duration, error)
}

// DefaultConfig uses the approved schedule and positive jitter up to twenty percent.
func DefaultConfig() Config {
	return Config{Backoff: [MaxAttempts]time.Duration{time.Minute, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour, 6 * time.Hour},
		CallTimeout: 15 * time.Second, FinishTimeout: 5 * time.Second, Jitter: positiveJitter}
}

func positiveJitter(base time.Duration) (time.Duration, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(base/5)+1))
	if err != nil {
		return 0, errors.New("refresh jitter unavailable")
	}
	return time.Duration(n.Int64()), nil
}

// Classify preserves ambiguity unless the adapter proves non-consumption or revocation.
func Classify(err error) string {
	var failure *port.Failure
	if !errors.As(err, &failure) || failure == nil {
		return FailureUnknown
	}
	if failure.Revoked {
		return FailureInvalid
	}
	if failure.NotConsumed && (failure.Timeout || failure.HTTPStatus == 429 || failure.HTTPStatus >= 500 && failure.HTTPStatus <= 599) {
		return FailureTemporary
	}
	return FailureUnknown
}

// NextRetry never schedules earlier than either backoff or Retry-After.
func (c Config) NextRetry(now time.Time, attempt int, retryAfter time.Time) (time.Time, error) {
	if attempt < 1 || attempt > MaxAttempts || c.Jitter == nil {
		return time.Time{}, errors.New("invalid refresh retry policy")
	}
	base := c.Backoff[attempt-1]
	if base <= 0 {
		return time.Time{}, errors.New("invalid refresh backoff")
	}
	jitter, err := c.Jitter(base)
	if err != nil || jitter < 0 || jitter > base/5 {
		return time.Time{}, errors.New("invalid refresh jitter")
	}
	next := now.UTC().Add(base + jitter)
	if retryAfter.After(next) {
		next = retryAfter.UTC()
	}
	return next, nil
}

// ProjectStatus treats expired access as expiring, independent of refresh eligibility.
func ProjectStatus(now time.Time, expires *time.Time) string {
	if expires != nil && !expires.After(now.Add(72*time.Hour)) {
		return "expiring"
	}
	return "active"
}
