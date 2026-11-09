package gateway

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/oklog/ulid/v2"
)

// CallService owns admission and terminal transactions; it never performs external IO in them.
type CallService struct {
	store  CallStore
	now    func() time.Time
	policy CallPolicy
}

// NewCallService requires durable persistence and an explicit bounded execution policy.
func NewCallService(store CallStore, now func() time.Time, policy CallPolicy) (*CallService, error) {
	if store == nil || now == nil || policy.Timeout <= 0 || policy.Timeout > time.Minute || policy.MaxAttempts < 1 || policy.MaxAttempts > 2 || len(policy.HashKey) < 32 {
		return nil, errors.New("gateway call dependencies and policy are required")
	}
	policy.HashKey = append([]byte(nil), policy.HashKey...)
	return &CallService{store: store, now: now, policy: policy}, nil
}

// NewCall freezes server identity, route version and UTC attribution before execution.
func (s *CallService) NewCall(p Principal, route Route, traceID, requestID, ip string) Call {
	now := s.now().UTC().Truncate(time.Microsecond)
	if !correlationID.MatchString(requestID) {
		requestID = ""
	}
	return Call{ID: ulid.Make().String(), TenantID: p.TenantID, ClientID: p.ClientID, RouteID: route.ID,
		RouteVersion: route.Version, TraceID: traceID, RequestID: requestID, Method: route.Method, Path: route.Path,
		IPHash: s.digest("ip", []byte(ip)), CalledAt: now, DeadlineAt: now.Add(s.policy.Timeout + time.Minute),
		State: "processing", Admitted: true, Result: json.RawMessage(`null`)}
}

// Start commits quota reservations and the processing log in the same transaction.
func (s *CallService) Start(ctx context.Context, call Call) error {
	err := s.store.Transaction(ctx, func(tx context.Context) error {
		inserted, err := s.store.InsertCall(tx, call)
		if err != nil {
			return err
		}
		if !inserted {
			current, err := s.store.Call(tx, call.ID)
			if err != nil || !sameCall(current, call) {
				return Unavailable()
			}
			return nil
		}
		if call.Admitted {
			return s.store.ConsumeQuota(tx, call)
		}
		return nil
	})
	if err == nil {
		return nil
	}
	// A lost commit acknowledgement is resolved using the original server ID.
	check, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	persisted, readErr := s.store.Call(check, call.ID)
	if readErr == nil && sameCall(persisted, call) {
		return nil
	}
	return err
}

// Finish commits a terminal log and its unique success meter event atomically.
func (s *CallService) Finish(ctx context.Context, id string, result CallResult) error {
	if !validCallResult(result) {
		return Unavailable()
	}
	err := s.store.Transaction(ctx, func(tx context.Context) error {
		call, err := s.store.Call(tx, id)
		if err != nil {
			return err
		}
		if call.State != "processing" {
			return terminalMatches(call, result)
		}
		finished := s.now().UTC()
		call.State, call.FinishedAt = "failed", &finished
		call.StatusCode, call.ErrorCode, call.Attempts = result.StatusCode, result.ErrorCode, result.Attempts
		if result.StatusCode >= 200 && result.StatusCode < 300 {
			if !call.Admitted {
				return Unavailable()
			}
			call.State, call.MeteredUnits, call.Result = "succeeded", 1, result.Data
			call.ResultHash = s.digest("result", result.Data)
		}
		if err := s.store.SaveCallResult(tx, call); err != nil {
			return err
		}
		if call.MeteredUnits == 1 {
			return s.store.InsertMeter(tx, call)
		}
		return nil
	})
	if err == nil {
		return nil
	}
	check, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	call, readErr := s.store.Call(check, id)
	if readErr == nil && call.State != "processing" && terminalMatches(call, result) == nil {
		return nil
	}
	return err
}

func (s *CallService) digest(purpose string, value []byte) string {
	mac := hmac.New(sha256.New, s.policy.HashKey)
	mac.Write([]byte(purpose + "\x00"))
	mac.Write(value)
	return hex.EncodeToString(mac.Sum(nil))
}

func validCallResult(r CallResult) bool {
	if r.Attempts < 0 || r.Attempts > 2 || r.StatusCode < 200 || r.StatusCode > 599 {
		return false
	}
	if r.StatusCode < 300 {
		return r.ErrorCode == "" && len(r.Data) <= 2<<20 && json.Valid(r.Data) && string(r.Data) != "null"
	}
	code, err := strconv.Atoi(r.ErrorCode)
	return err == nil && code >= 10000 && code <= 99999 && len(r.Data) == 0
}

func sameCall(a, b Call) bool {
	return a.TenantID == b.TenantID && a.ClientID == b.ClientID && a.RouteID == b.RouteID && a.CalledAt.Equal(b.CalledAt) && a.Admitted == b.Admitted
}

func terminalMatches(call Call, result CallResult) error {
	if call.StatusCode == result.StatusCode && call.ErrorCode == result.ErrorCode && (call.State == "failed" || call.State == "succeeded" && jsonEqual(call.Result, result.Data)) {
		return nil
	}
	return Unavailable()
}

func jsonEqual(a, b json.RawMessage) bool {
	var left, right any
	if json.Unmarshal(a, &left) != nil || json.Unmarshal(b, &right) != nil {
		return false
	}
	l, lerr := json.Marshal(left)
	r, rerr := json.Marshal(right)
	return lerr == nil && rerr == nil && string(l) == string(r)
}
