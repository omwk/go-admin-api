package bootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"

	"github.com/omwk/go-admin-api/internal/kernel/outbox"
	syncmodule "github.com/omwk/go-admin-api/internal/modules/sync"
)

func syncEventHandler(engine *syncmodule.Orchestrator, fallback func(context.Context, outbox.Event) error) func(context.Context, outbox.Event) error {
	return func(ctx context.Context, event outbox.Event) error {
		if event.EventType != "sync.dispatch" {
			return fallback(ctx, event)
		}
		var payload struct {
			JobID    int64 `json:"job_id"`
			RunID    int64 `json:"run_id"`
			Sequence int   `json:"sequence"`
		}
		decoder := json.NewDecoder(bytes.NewReader(event.Payload))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&payload); err != nil {
			return errors.New("invalid sync dispatch identifiers")
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			return errors.New("invalid sync dispatch trailing data")
		}
		if payload.JobID <= 0 || payload.RunID <= 0 || payload.Sequence < 0 || event.TenantID <= 0 || event.AggregateType != "sync_run" || event.AggregateID != strconv.FormatInt(payload.RunID, 10) {
			return errors.New("invalid sync dispatch identity")
		}
		return engine.ExecuteDispatch(ctx, payload.JobID, payload.RunID, event.TenantID, payload.Sequence)
	}
}
