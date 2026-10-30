// Package audit writes append-only audit records through an injected database executor.
package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"

	"github.com/omwk/go-admin-api/internal/kernel/logger"
)

const insertStatement = `INSERT INTO audit_log
	(tenant_id, actor_type, actor_id, action, resource_type, resource_id, "before", "after", ip, user_agent, trace_id, result)
	VALUES (?, ?, ?, ?, ?, ?, ?::jsonb, ?::jsonb, NULLIF(?, '')::inet, ?, ?, ?)`

// Executor is implemented by kernel/db and by focused unit-test doubles.
type Executor interface {
	Exec(context.Context, string, ...any) error
}

// Event contains a sanitized audit event; Before and After must exclude secrets and PII.
type Event struct {
	TenantID     int64
	ActorType    string
	ActorID      *int64
	Action       string
	ResourceType string
	ResourceID   string
	Before       map[string]any
	After        map[string]any
	IP           string
	UserAgent    string
	Result       string
}

// Record validates and appends an event using the trace identifier from ctx.
func Record(ctx context.Context, executor Executor, event Event) error {
	if executor == nil {
		return errors.New("audit executor is required")
	}
	traceID := logger.TraceID(ctx)
	if err := validate(event, traceID); err != nil {
		return err
	}
	before, err := marshal(event.Before)
	if err != nil {
		return fmt.Errorf("encode audit before state: %w", err)
	}
	after, err := marshal(event.After)
	if err != nil {
		return fmt.Errorf("encode audit after state: %w", err)
	}
	var ip any
	if event.IP != "" {
		ip = event.IP
	}
	if err := executor.Exec(ctx, insertStatement,
		event.TenantID, event.ActorType, event.ActorID, event.Action,
		event.ResourceType, event.ResourceID, before, after, ip,
		event.UserAgent, traceID, event.Result,
	); err != nil {
		return fmt.Errorf("write audit record: %w", err)
	}
	return nil
}

func validate(event Event, traceID string) error {
	if event.TenantID <= 0 || event.Action == "" || event.ResourceType == "" || event.ResourceID == "" || traceID == "" {
		return errors.New("audit tenant, action, resource and trace are required")
	}
	if event.ActorType != "user" && event.ActorType != "system" && event.ActorType != "api_client" {
		return errors.New("invalid audit actor type")
	}
	if event.Result != "success" && event.Result != "failure" {
		return errors.New("invalid audit result")
	}
	if event.IP != "" && net.ParseIP(event.IP) == nil {
		return errors.New("invalid audit ip address")
	}
	return nil
}

func marshal(value map[string]any) ([]byte, error) {
	if value == nil {
		return []byte("{}"), nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, errors.New("audit state is not valid JSON")
	}
	return encoded, nil
}
