package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
)

// Run starts one durable invocation and keeps its identity across bounded read retries.
func (s *CallService) Run(ctx context.Context, call Call, execute func(context.Context) (OpenOrderStatus, error)) (json.RawMessage, error) {
	if err := s.Start(ctx, call); err != nil {
		return nil, s.Reject(ctx, call, err)
	}
	work, cancel := context.WithTimeout(context.WithValue(ctx, callContextKey{}, call.ID), s.policy.Timeout)
	defer cancel()
	data, attempts, err := s.execute(work, call.Method, execute)
	result := CallResult{StatusCode: 200, Data: data, Attempts: attempts}
	if err != nil {
		app := errs.From(PublicError(err))
		result.StatusCode, result.ErrorCode, result.Data = app.HTTPStatus, strconv.Itoa(app.Code), nil
	}
	finishCtx := work
	if err != nil {
		// Cancellation must not discard the durable failure record.
		var release context.CancelFunc
		finishCtx, release = context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer release()
	}
	if finishErr := s.Finish(finishCtx, call.ID, result); finishErr != nil {
		return nil, Unavailable()
	}
	return data, err
}

func (s *CallService) execute(ctx context.Context, method string, execute func(context.Context) (OpenOrderStatus, error)) (data json.RawMessage, attempts int, err error) {
	defer func() {
		if recover() != nil {
			data, err = nil, errs.New(10002, 500, "服务处理失败")
		}
	}()
	for attempts < s.policy.MaxAttempts {
		if err := ctx.Err(); err != nil {
			return nil, attempts, errs.New(80302, 504, "服务响应超时")
		}
		attempts++
		var value OpenOrderStatus
		value, err = execute(ctx)
		if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return nil, attempts, errs.New(80302, 504, "服务响应超时")
		}
		if err == nil {
			data, err = json.Marshal(value)
			return data, attempts, err
		}
		app := errs.From(PublicError(err))
		if method != "GET" && method != "HEAD" || app.Code != 80301 || app.HTTPStatus != 502 {
			break
		}
	}
	return nil, attempts, err
}

// Reject records an authenticated denial without reserving any business quota.
func (s *CallService) Reject(ctx context.Context, call Call, cause error) error {
	call.Admitted = false
	bounded, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if err := s.Start(bounded, call); err != nil {
		return Unavailable()
	}
	app := errs.From(PublicError(cause))
	if err := s.Finish(bounded, call.ID, CallResult{StatusCode: app.HTTPStatus, ErrorCode: strconv.Itoa(app.Code)}); err != nil {
		return Unavailable()
	}
	return cause
}

// Reconcile fails expired processing calls without creating success metering.
func (s *CallService) Reconcile(ctx context.Context) (int, error) {
	calls, err := s.store.ExpiredCalls(ctx, s.now().UTC())
	if err != nil {
		return 0, err
	}
	completed := 0
	for _, call := range calls {
		if err := s.Finish(ctx, call.ID, CallResult{StatusCode: 504, ErrorCode: "80302"}); err != nil {
			current, readErr := s.store.Call(ctx, call.ID)
			if readErr != nil || current.State == "processing" {
				return completed, err
			}
			continue
		}
		completed++
	}
	return completed, nil
}

// Usage queries only the authenticated application and a strict UTC calendar date.
func (s *CallService) Usage(ctx context.Context, p Principal, date string) (Usage, error) {
	if date == "" {
		date = s.now().UTC().Format("2006-01-02")
	}
	from, err := time.Parse("2006-01-02", date)
	if err != nil || from.Format("2006-01-02") != date {
		return Usage{}, Invalid()
	}
	usage, err := s.store.Usage(ctx, p, from, from.AddDate(0, 0, 1))
	usage.Date, usage.AsOf = date, s.now().UTC()
	return usage, err
}
