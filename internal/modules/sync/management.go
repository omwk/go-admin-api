package sync

import (
	"context"
	"errors"

	"github.com/omwk/go-admin-api/internal/kernel/idem"
	"github.com/omwk/go-admin-api/internal/modules/sync/repo"
)

// Management applies current human authorization before every management query.
type Management struct {
	store  repo.ManagementStore
	engine *Orchestrator
}

func NewManagement(store repo.ManagementStore, engine *Orchestrator) (*Management, error) {
	if store == nil || engine == nil {
		return nil, errors.New("sync management dependencies required")
	}
	return &Management{store: store, engine: engine}, nil
}

func (s *Management) start(ctx context.Context, id int64, kind string, op *idem.Operation) (repo.Run, error) {
	start := repo.Start{JobID: id, Trigger: "manual", Operation: op}
	if kind != "" {
		var err error
		start, err = s.store.ReplaySource(ctx, kind, id)
		if err != nil {
			return repo.Run{}, err
		}
		start.Operation = op
	}
	return s.engine.Start(ctx, start)
}
