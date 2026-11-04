package sync

import (
	"context"
	"encoding/json"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/idem"
	"github.com/omwk/go-admin-api/internal/modules/sync/repo"
)

// ReplayScope selects retained raw by a half-open time range, IDs, or quarantine.
type ReplayScope struct {
	Type   string     `json:"type"`
	From   *time.Time `json:"from,omitempty"`
	To     *time.Time `json:"to,omitempty"`
	IDs    []int64    `json:"ids,omitempty"`
	FromID int64      `json:"from_raw_id,omitempty"`
	ToID   int64      `json:"to_raw_id,omitempty"`
}

// ReplayRequest freezes an explicit mapping release and raw selection.
type ReplayRequest struct {
	Resource         string      `json:"resource"`
	ShopID           int64       `json:"shop_id"`
	MappingVersionID int64       `json:"mapping_version_id"`
	Scope            ReplayScope `json:"scope"`
}

// ReplayKey is a complete partition-aware raw identity.
type ReplayKey struct {
	ID        int64     `json:"id"`
	FetchedAt time.Time `json:"fetched_at"`
}

// ReplayStats partitions all committed member outcomes.
type ReplayStats struct {
	Inserted     int64 `json:"inserted"`
	Updated      int64 `json:"updated"`
	SkippedStale int64 `json:"skipped_stale"`
	SkippedSame  int64 `json:"skipped_same"`
	Quarantined  int64 `json:"quarantined"`
	Failed       int64 `json:"failed"`
}

// Processed returns the conserved count of all dispositions.
func (s ReplayStats) Processed() int64 {
	return s.Inserted + s.Updated + s.SkippedStale + s.SkippedSame + s.Quarantined + s.Failed
}

// ReplayRun contains immutable membership and mapping facts plus durable progress.
type ReplayRun struct {
	ETASeconds              *int64          `json:"eta_seconds" gorm:"-"`
	LastCommittedAt         *time.Time      `json:"last_committed_at" gorm:"-"`
	ID                      int64           `json:"id"`
	TenantID                int64           `json:"tenant_id"`
	PlatformID              int64           `json:"platform_id"`
	ShopID                  int64           `json:"shop_id"`
	ResourceType            string          `json:"resource"`
	MappingVersionID        int64           `json:"mapping_version_id"`
	MappingChecksum         string          `json:"mapping_checksum"`
	MappingSpec             json.RawMessage `json:"-" gorm:"type:jsonb"`
	FunctionVersion         string          `json:"function_version"`
	AuthorizationGeneration int64           `json:"-"`
	SchedulingGeneration    int64           `json:"-"`
	FromRawID               int64           `json:"from_raw_id"`
	ToRawID                 int64           `json:"to_raw_id"`
	Total                   int64           `json:"total"`
	Processed               int64           `json:"processed"`
	CheckpointKey           json.RawMessage `json:"checkpoint_key" gorm:"type:jsonb"`
	UpperKey                json.RawMessage `json:"upper_key" gorm:"type:jsonb"`
	Stats                   ReplayStats     `json:"stats" gorm:"serializer:json;type:jsonb"`
	Status                  string          `json:"status"`
	TriggeredBy             int64           `json:"triggered_by"`
	StartedAt               time.Time       `json:"started_at"`
	FinishedAt              *time.Time      `json:"finished_at"`
}

// ReplayStore applies live human authorization before freeze and reads.
type ReplayStore interface {
	Authorize(context.Context, int64, int64, string) (context.Context, error)
	CreateReplay(context.Context, ReplayRequest, *idem.Operation) (ReplayRun, error)
	ReadReplay(context.Context, int64) (ReplayRun, error)
	ListReplays(context.Context, repo.PageQuery) (repo.Result, error)
	CancelReplay(context.Context, int64, *idem.Operation) (ReplayRun, error)
}
