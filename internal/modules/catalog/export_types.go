package catalog

import (
	"context"
	"encoding/json"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/idem"
)

// ExportBudget bounds one worker attempt, including IO and publication.
const ExportBudget = 5 * time.Minute

// ExportRequest allows filters and sort only; object keys are server-owned.
type ExportRequest struct {
	Filters ProductFilter `json:"filters"`
	Sort    string        `json:"sort,omitempty"`
}

// ExportSnapshot freezes the query and output field version at creation.
type ExportSnapshot struct {
	ExportRequest
	FieldsVersion int `json:"fields_version"`
}

// ExportRow contains only the immutable, whitelisted CSV projection.
type ExportRow struct {
	Ordinal     int64
	Projection  json.RawMessage
	GeneratedAt time.Time
}

// ExportProjection deliberately excludes raw data and all extension maps.
type ExportProjection struct {
	ID            int64     `json:"id"`
	Title         string    `json:"title"`
	PriceMinCents int64     `json:"price_min_cents"`
	PriceMaxCents int64     `json:"price_max_cents"`
	SKUCount      int       `json:"sku_count"`
	CategoryID    *int64    `json:"category_id"`
	Category      string    `json:"category"`
	Status        string    `json:"status"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// ExportDigest describes the exact bytes and data records, excluding the header.
type ExportDigest struct {
	Size   int64
	Rows   int64
	SHA256 string
}

// ExportStore implements atomic materialization, publication and cleanup guards.
type ExportStore interface {
	CreateExport(context.Context, int64, int64, ExportSnapshot, *idem.Operation) (ExportJob, error)
	ReadExport(context.Context, int64, int64, int64) (ExportJob, error)
	BeginExport(context.Context, int64, int64) (ExportJob, error)
	ExportRows(context.Context, ExportJob, int64) ([]ExportRow, error)
	PublishExport(context.Context, ExportJob, string, ExportDigest) (bool, error)
	FailExport(context.Context, ExportJob) error
	CanDeleteExportObject(context.Context, string) (bool, error)
	QueueExportCleanup(context.Context, int64, string) error
}

// ExportStatus adds an expiring download URL to the safe job response.
type ExportStatus struct {
	ExportJob
	DownloadURL       string     `json:"download_url,omitempty"`
	DownloadExpiresAt *time.Time `json:"download_expires_at,omitempty"`
}
