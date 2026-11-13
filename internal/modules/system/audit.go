package system

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"
	"unicode/utf8"
)

// AuditFilter describes validated tenant audit filters, using inclusive time bounds.
type AuditFilter struct {
	ActorID              int64
	Action, ResourceType string
	ResourceID, Result   string
	From, To             *time.Time
	Page, PageSize       int
}

// AuditRecord is the persisted audit projection; Username is only used for login history.
type AuditRecord struct {
	ID           int64           `json:"id"`
	ActorType    string          `json:"actor_type"`
	ActorID      *int64          `json:"actor_id"`
	Action       string          `json:"action"`
	ResourceType string          `json:"resource_type"`
	ResourceID   string          `json:"resource_id"`
	Before       json.RawMessage `json:"before"`
	After        json.RawMessage `json:"after"`
	IP           string          `json:"ip"`
	UserAgent    string          `json:"user_agent"`
	TraceID      string          `json:"trace_id"`
	Result       string          `json:"result"`
	CreatedAt    time.Time       `json:"created_at"`
	Username     string          `json:"-"`
}

// AuditLog contains sanitized, bounded state summaries.
type AuditLog struct {
	AuditRecord
	Truncated bool `json:"truncated"`
}

// LoginLog omits snapshots and preserves unknown account anonymity.
type LoginLog struct {
	ID         int64     `json:"id"`
	UserID     *int64    `json:"user_id"`
	Username   string    `json:"username"`
	ResourceID string    `json:"resource_id"`
	Result     string    `json:"result"`
	IP         string    `json:"ip"`
	UserAgent  string    `json:"user_agent"`
	CreatedAt  time.Time `json:"created_at"`
}

// AuditReader reads audit rows without mutating the append-only log.
type AuditReader interface {
	AuditRecords(context.Context, int64, AuditFilter) ([]AuditRecord, int64, error)
}

// AuditService owns safe read projections of the existing audit log.
type AuditService struct{ store AuditReader }

// NewAuditService requires the tenant-bound read port.
func NewAuditService(store AuditReader) (*AuditService, error) {
	if store == nil {
		return nil, errors.New("audit reader is required")
	}
	return &AuditService{store: store}, nil
}

// AuditLogs returns redacted snapshots with at most 4 KiB combined per entry.
func (s *AuditService) AuditLogs(ctx context.Context, actor Actor, filter AuditFilter) (Page[AuditLog], error) {
	out := Page[AuditLog]{List: []AuditLog{}, Page: filter.Page, PageSize: filter.PageSize}
	if err := validateAuditFilter(actor, filter); err != nil {
		return out, err
	}
	rows, total, err := s.store.AuditRecords(ctx, actor.TenantID, filter)
	if err != nil {
		return out, err
	}
	out.Total = total
	for _, row := range rows {
		before, after, truncated, err := auditSummaries(row.Before, row.After)
		if err != nil {
			return Page[AuditLog]{}, err
		}
		row.Before, row.After, row.CreatedAt = before, after, row.CreatedAt.UTC()
		out.List = append(out.List, AuditLog{AuditRecord: row, Truncated: truncated})
	}
	return out, nil
}

// LoginLogs fixes the source action and never infers unknown identities from snapshots.
func (s *AuditService) LoginLogs(ctx context.Context, actor Actor, filter AuditFilter) (Page[LoginLog], error) {
	out := Page[LoginLog]{List: []LoginLog{}, Page: filter.Page, PageSize: filter.PageSize}
	filter.Action = "identity.login"
	if err := validateAuditFilter(actor, filter); err != nil {
		return out, err
	}
	rows, total, err := s.store.AuditRecords(ctx, actor.TenantID, filter)
	if err != nil {
		return out, err
	}
	out.Total = total
	for _, row := range rows {
		item := LoginLog{ID: row.ID, Username: "未知账号", ResourceID: "unknown",
			Result: row.Result, IP: row.IP, UserAgent: row.UserAgent, CreatedAt: row.CreatedAt.UTC()}
		if id, err := strconv.ParseInt(row.ResourceID, 10, 64); err == nil && id > 0 {
			item.UserID, item.ResourceID = &id, row.ResourceID
			if row.Username != "" {
				item.Username = row.Username
			}
		}
		out.List = append(out.List, item)
	}
	return out, nil
}

func validateAuditFilter(actor Actor, filter AuditFilter) error {
	if actor.TenantID <= 0 || filter.ActorID < 0 || filter.Page < 1 || filter.Page > 10000 || filter.PageSize < 1 || filter.PageSize > 100 {
		return invalidHTTP()
	}
	if filter.Result != "" && filter.Result != "success" && filter.Result != "failure" {
		return invalidHTTP()
	}
	if utf8.RuneCountInString(filter.Action) > 128 || utf8.RuneCountInString(filter.ResourceType) > 64 {
		return invalidHTTP()
	}
	if filter.From != nil && filter.To != nil && filter.From.After(*filter.To) {
		return invalidHTTP()
	}
	return nil
}
