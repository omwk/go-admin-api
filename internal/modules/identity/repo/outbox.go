package repo

import "context"

// OutboxEmitter persists authorization invalidation events in the active transaction.
type OutboxEmitter interface {
	EmitAuthorizationInvalidation(context.Context, int64, []int64) error
}
