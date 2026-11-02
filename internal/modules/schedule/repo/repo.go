package repo

import "context"

// EnabledJob is the minimal scheduler projection. It contains no job options.
type EnabledJob struct {
	JobID int64
	Cron  string
}

// Store is the dedicated bootstrap scheduler query port.
type Store interface {
	ListEnabledJobs(context.Context, int64) ([]EnabledJob, error)
}
