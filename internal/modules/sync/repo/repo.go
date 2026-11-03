package repo

import "context"

// ResolvedTask is the verified durable identity needed to issue WorkAccess.
type ResolvedTask struct {
	TenantID       int64
	JobID          int64
	RunID          int64
	ShopID         int64
	Resource       string
	MappingVersion int64
	Generation     int64
}

// Store is the dedicated bootstrap task resolution port.
type Store interface {
	ResolveTask(context.Context, int64, int64, int64) (ResolvedTask, error)
}
