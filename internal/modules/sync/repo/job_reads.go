package repo

import "time"

// JobView exposes schedule and checkpoint metadata without executable options.
type JobView struct {
	ID              int64      `json:"id"`
	ShopID          int64      `json:"shop_id"`
	ResourceType    string     `json:"resource_type"`
	Cron            string     `json:"cron"`
	Enabled         bool       `json:"enabled"`
	LastRunID       *int64     `json:"last_run_id"`
	LastRunStatus   *string    `json:"last_run_status"`
	CursorType      string     `json:"cursor_type"`
	CursorValue     string     `json:"cursor_value"`
	CursorUpdatedAt *time.Time `json:"cursor_updated_at"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}
