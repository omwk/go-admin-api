package repo

import (
	"context"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/idem"
)

type DriftEvent struct {
	ID          int64     `json:"id"`
	ShopID      int64     `json:"shop_id"`
	BaselineID  int64     `json:"baseline_id"`
	Kind        string    `json:"kind"`
	Path        string    `json:"path"`
	OldType     string    `json:"old_type"`
	NewType     string    `json:"new_type"`
	Severity    string    `json:"severity"`
	Status      string    `json:"status"`
	SampleRawID int64     `json:"sample_raw_id"`
	DetectedAt  time.Time `json:"detected_at"`
}

type DriftStore interface {
	ListDrifts(context.Context, PageQuery) (Result, error)
	ChangeDrift(context.Context, int64, string, *idem.Operation) error
	PromoteBaseline(context.Context, int64, *idem.Operation) error
}
