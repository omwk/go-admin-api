package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/omwk/go-admin-api/internal/infra/storage"
	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/kernel/outbox"
	"github.com/omwk/go-admin-api/internal/modules/file/model"
)

type fileCleanup struct {
	ProviderID *int64 `json:"provider_id"`
	Bucket     string `json:"bucket"`
	Key        string `json:"key"`
	ThumbKey   string `json:"thumb_key,omitempty"`
}

func (s *fileStore) QueueFileCleanup(ctx context.Context, row model.FileObject) error {
	if row.ThumbStatus == "pending" {
		row.ThumbKey = row.ObjectKey + ".thumb.jpg"
	}
	payload, err := json.Marshal(fileCleanup{ProviderID: row.ProviderID, Bucket: row.Bucket, Key: row.ObjectKey, ThumbKey: row.ThumbKey})
	if err != nil {
		return err
	}
	return outbox.Emit(ctx, s.database, outbox.Event{TenantID: row.TenantID, AggregateType: "file_object", AggregateID: strconv.FormatInt(row.ID, 10), EventType: "file.cleanup", Payload: payload})
}

func fileEventHandler(resolver *storage.Resolver, fallback func(context.Context, outbox.Event) error) func(context.Context, outbox.Event) error {
	cleanup := fileCleanupHandler(resolver)
	return func(ctx context.Context, event outbox.Event) error {
		if event.EventType != "file.cleanup" {
			return fallback(ctx, event)
		}
		return cleanup(ctx, event)
	}
}

func fileCleanupHandler(resolver *storage.Resolver) func(context.Context, outbox.Event) error {
	return func(ctx context.Context, event outbox.Event) error {
		if db.InTransaction(ctx) {
			return errors.New("file cleanup requires committed metadata")
		}
		id, err := strconv.ParseInt(event.AggregateID, 10, 64)
		var payload fileCleanup
		if err != nil || id <= 0 || event.TenantID <= 0 || event.EventType != "file.cleanup" || event.AggregateType != "file_object" || json.Unmarshal(event.Payload, &payload) != nil || payload.Bucket == "" || !strings.HasPrefix(payload.Key, "tenants/"+strconv.FormatInt(event.TenantID, 10)+"/") {
			return errors.New("invalid file cleanup identity")
		}
		selected, err := resolver.Resolve(ctx, payload.ProviderID)
		if err != nil {
			return err
		}
		object := selected.Object.WithBucket(payload.Bucket)
		if payload.ThumbKey != "" {
			if payload.ThumbKey != payload.Key+".thumb.jpg" {
				return errors.New("invalid thumbnail cleanup identity")
			}
			if err := object.Delete(ctx, payload.ThumbKey); err != nil {
				return err
			}
		}
		return object.Delete(ctx, payload.Key)
	}
}
