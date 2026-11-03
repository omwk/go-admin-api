package repo

import (
	"context"
	"encoding/json"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/idem"
	"github.com/omwk/go-admin-api/internal/kernel/scope"
	"github.com/omwk/go-admin-api/internal/modules/ingestion"
)

// Start contains trusted invocation metadata, never queue-carried authorization.
type Start struct {
	Operation  *idem.Operation
	JobID      int64
	Trigger    string
	Submission string
	ReplayKind string
	ReplayID   int64
}

type Job struct {
	ID, TenantID, ShopID, PlatformID, MappingVersion, Generation int64
	Resource, PlatformCode                                       string
	MaxPages, PageSize                                           int
}

// Stats also carries durable continuation metadata for this run chain.
type Stats struct {
	Fetched        int    `json:"fetched"`
	Created        int    `json:"created"`
	Updated        int    `json:"updated"`
	Skipped        int    `json:"skipped"`
	Quarantined    int    `json:"quarantined"`
	Failed         int    `json:"failed"`
	MappingVersion int64  `json:"mapping_version_id"`
	Generation     int64  `json:"authorization_generation"`
	Pending        bool   `json:"pending"`
	Sequence       int    `json:"sequence"`
	Dispatch       int    `json:"dispatch"`
	Retries        int    `json:"retries"`
	Submission     string `json:"submission,omitempty"`
	ReplayKind     string `json:"replay_kind,omitempty"`
	ReplayID       int64  `json:"replay_id,omitempty"`
}

type Run struct {
	ID           int64      `json:"id"`
	TenantID     int64      `json:"tenant_id"`
	JobID        int64      `json:"job_id"`
	ShopID       int64      `json:"shop_id"`
	ResourceType string     `json:"resource_type"`
	Trigger      string     `json:"trigger"`
	Status       string     `json:"status"`
	Stats        Stats      `json:"stats" gorm:"serializer:json"`
	TriggeredBy  *int64     `json:"triggered_by"`
	StartedAt    time.Time  `json:"started_at"`
	FinishedAt   *time.Time `json:"finished_at"`
	ErrorCode    string     `json:"error_code"`
	ErrorMsg     string     `json:"error_msg"`
	TraceID      string     `json:"trace_id"`
	ShopName     string     `json:"shop_name" gorm:"->"`
	PlatformName string     `json:"platform_name" gorm:"->"`
	Errors       []RunError `json:"errors,omitempty" gorm:"-"`
}

// RunError exposes only the safe, normalized error details of a scoped run.
type RunError struct {
	ID        int64     `json:"id"`
	Stage     string    `json:"stage"`
	ErrorCode string    `json:"error_code"`
	Message   string    `json:"message"`
	Retryable bool      `json:"retryable"`
	RawID     *int64    `json:"raw_id"`
	CreatedAt time.Time `json:"created_at"`
}

type Work struct {
	Job     Job
	Run     Run
	Access  scope.WorkAccess
	Mapping *ingestion.Compiled
}

type Cursor struct {
	Value     string
	UpdatedAt time.Time
}
type Disposition struct {
	Mapped ingestion.Mapped
	Raw    ingestion.RawRef
	Reason string
	Dead   bool
}
type Page struct {
	Run            Run
	Before, Next   string
	Items          []Disposition
	HasMore, Yield bool
}

// EngineStore is the worker-only, capability-checked persistence port. ResolveJob
// and CreateRun are dedicated bootstrap entries before a capability exists.
type EngineStore interface {
	ResolveJob(context.Context, int64) (Job, error)
	CreateRun(context.Context, Start) (Run, error)
	LoadWork(context.Context, int64, int64) (Work, error)
	LoadCursor(context.Context) (Cursor, error)
	CommitPage(context.Context, Page) (Run, error)
	DeferRun(context.Context, Run, string, time.Duration, bool) error
}

type PageQuery struct {
	Number, Size  int
	Status        string
	ShopID, JobID int64
}
type Record struct {
	ID           int64     `json:"id"`
	ShopID       int64     `json:"shop_id"`
	ResourceType string    `json:"resource_type"`
	ExternalID   string    `json:"external_id"`
	Reason       string    `json:"reason"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
}
type Result struct {
	Items    any   `json:"list"`
	Total    int64 `json:"total"`
	Page     int   `json:"page"`
	PageSize int   `json:"page_size"`
}

// ManagementStore always requires a current human shop scope, including counts.
type ManagementStore interface {
	Authorize(context.Context, int64, int64, string) (context.Context, error)
	ListRuns(context.Context, PageQuery) (Result, error)
	ReadRun(context.Context, int64) (Run, error)
	ListRecords(context.Context, string, PageQuery) (Result, error)
	ReplaySource(context.Context, string, int64) (Start, error)
}

// ReplayRaw is metadata only; the original payload is reread from raw storage.
type ReplayRaw struct {
	RawID          int64     `json:"raw_id"`
	FetchedAt      time.Time `json:"fetched_at"`
	MappingVersion int64     `json:"mapping_version_id"`
	JobID          int64     `json:"job_id"`
	RunID          int64     `json:"run_id"`
}

type ReplayStore interface {
	LoadReplay(context.Context, Run) (json.RawMessage, ingestion.RawRef, error)
}
