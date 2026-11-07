package ingestion

import (
	"context"
	"encoding/json"
	"time"

	"gorm.io/gorm"
)

// ResourceType identifies the canonical resource being interpreted.
type ResourceType string

const (
	ResourceProduct   ResourceType = "product"
	ResourceOrder     ResourceType = "order"
	ResourceInventory ResourceType = "inventory"
	ResourceCategory  ResourceType = "category"
)

// FieldSpec describes one source-to-target mapping.
type FieldSpec struct {
	Target    string          `json:"target"`
	Source    string          `json:"source"`
	Required  bool            `json:"required"`
	Transform string          `json:"transform"`
	Default   json.RawMessage `json:"default"`
}

// CollectionSpec describes one level of child records.
type CollectionSpec struct {
	Target   string      `json:"target"`
	Source   string      `json:"source"`
	KeyField string      `json:"key_field"`
	Fields   []FieldSpec `json:"fields"`
}

// Spec is the versioned mapping DSL persisted in mapping_version.spec.
type Spec struct {
	SchemaVersion int                          `json:"schema_version"`
	Resource      string                       `json:"resource"`
	KeyField      string                       `json:"key_field"`
	Fields        []FieldSpec                  `json:"fields"`
	Collections   []CollectionSpec             `json:"collections"`
	StatusTables  map[string]map[string]string `json:"status_tables"`
}

// FieldError describes a record-level mapping problem.
type FieldError struct {
	Target  string `json:"target"`
	Source  string `json:"source"`
	Kind    string `json:"kind"`
	Message string `json:"message"`
	Fatal   bool   `json:"fatal"`
}

// Mapped is the transport object handed to a canonical target upserter.
type Mapped struct {
	Replay          bool
	TenantID        int64
	ShopID          int64
	PlatformID      int64
	ExternalID      string
	Values          map[string]any
	Collections     map[string][]map[string]any
	RawID           int64
	SourceFetchedAt time.Time
	SourceOrder     int64
	MappingChecksum string
}

// UpsertStats reports canonical persistence results.
type UpsertStats struct {
	Inserted     int
	Updated      int
	Skipped      int
	Quarantined  int
	SkippedStale int
	SkippedSame  int
	Reason       string
}

// TargetUpserter persists mapped canonical records in the caller's transaction.
type TargetUpserter interface {
	Upsert(ctx context.Context, tx *gorm.DB, resource ResourceType, records []Mapped) (UpsertStats, error)
}
