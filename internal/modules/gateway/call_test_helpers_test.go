package gateway

import (
	"context"
	"crypto/rand"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type callTestStore struct {
	calls      map[string]Call
	meters     map[string]int
	admissions int
	meterError error
	loseCommit bool
}

func newCallTestService(t *testing.T) (*CallService, *callTestStore) {
	t.Helper()
	store := &callTestStore{calls: map[string]Call{}, meters: map[string]int{}}
	key := make([]byte, 32)
	_, err := rand.Read(key)
	require.NoError(t, err)
	svc, err := NewCallService(store, func() time.Time { return time.Unix(1800000000, 0).UTC() }, CallPolicy{Timeout: time.Second, MaxAttempts: 2, HashKey: key})
	require.NoError(t, err)
	return svc, store
}

func (s *callTestStore) Transaction(ctx context.Context, fn func(context.Context) error) error {
	calls, meters, admissions := map[string]Call{}, map[string]int{}, s.admissions
	for k, v := range s.calls {
		calls[k] = v
	}
	for k, v := range s.meters {
		meters[k] = v
	}
	if err := fn(ctx); err != nil {
		s.calls, s.meters, s.admissions = calls, meters, admissions
		return err
	}
	if s.loseCommit {
		s.loseCommit = false
		return Unavailable()
	}
	return nil
}

func (s *callTestStore) InsertCall(_ context.Context, call Call) (bool, error) {
	if _, ok := s.calls[call.ID]; ok {
		return false, nil
	}
	s.calls[call.ID] = call
	return true, nil
}

func (s *callTestStore) Call(_ context.Context, id string) (Call, error) {
	call, ok := s.calls[id]
	if !ok {
		return Call{}, ResourceUnavailable()
	}
	return call, nil
}

func (s *callTestStore) ConsumeQuota(context.Context, Call) error { s.admissions++; return nil }
func (s *callTestStore) SaveCallResult(_ context.Context, call Call) error {
	s.calls[call.ID] = call
	return nil
}
func (s *callTestStore) InsertMeter(_ context.Context, call Call) error {
	if s.meterError != nil {
		return s.meterError
	}
	s.meters[call.ID]++
	return nil
}
func (s *callTestStore) ExpiredCalls(_ context.Context, now time.Time) ([]Call, error) {
	var out []Call
	for _, call := range s.calls {
		if call.State == "processing" && !call.DeadlineAt.After(now) {
			out = append(out, call)
		}
	}
	return out, nil
}
func (*callTestStore) Usage(context.Context, Principal, time.Time, time.Time) (Usage, error) {
	return Usage{}, nil
}

type openTestLimiter struct{}

func (openTestLimiter) Take(context.Context, Principal, int64) (RateDecision, error) {
	return RateDecision{LimitType: "client", Limit: 100, Remaining: 99}, nil
}
