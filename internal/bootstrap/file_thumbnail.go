package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"github.com/omwk/go-admin-api/internal/infra/storage"
	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/kernel/outbox"
	filemodule "github.com/omwk/go-admin-api/internal/modules/file"
	"github.com/omwk/go-admin-api/internal/modules/file/model"
	"github.com/omwk/go-admin-api/internal/worker"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *fileStore) queueThumbnail(ctx context.Context, row model.FileObject) error {
	payload, err := json.Marshal(worker.ThumbnailPayload{TenantID: row.TenantID, FileID: row.ID})
	if err != nil {
		return err
	}
	return outbox.Emit(ctx, s.database, outbox.Event{TenantID: row.TenantID, AggregateType: "file_object", AggregateID: strconv.FormatInt(row.ID, 10), EventType: "thumb.generate", Payload: payload})
}

func thumbnailEventHandler(publisher *worker.QueuePublisher, next func(context.Context, outbox.Event) error) func(context.Context, outbox.Event) error {
	return func(ctx context.Context, event outbox.Event) error {
		if event.EventType != "thumb.generate" {
			return next(ctx, event)
		}
		var payload worker.ThumbnailPayload
		if json.Unmarshal(event.Payload, &payload) != nil || payload.TenantID <= 0 || payload.FileID <= 0 || payload.TenantID != event.TenantID || event.AggregateType != "file_object" || event.AggregateID != strconv.FormatInt(payload.FileID, 10) {
			return errors.New("invalid thumbnail event identity")
		}
		return publisher.EnqueueThumbnail(ctx, payload)
	}
}

func (s *fileStore) CompleteThumbnail(ctx context.Context, tenant, id int64, key, status string) error {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	result := q.Model(&model.FileObject{}).Where("tenant_id=? AND id=? AND thumb_status='pending'", tenant, id).
		Updates(map[string]any{"thumb_key": key, "thumb_status": status})
	if result.Error != nil {
		return fileDBError(result.Error)
	}
	if result.RowsAffected == 0 {
		_, err := s.File(ctx, tenant, id)
		return err
	}
	return nil
}

func (s *fileStore) requeueThumbnails(ctx context.Context, now time.Time, after int64) (int64, error) {
	last := after
	err := s.FileTransaction(ctx, func(tx context.Context) error {
		q, cancel := s.database.WithContext(tx)
		defer cancel()
		var rows []model.FileObject
		err := q.Model(&model.FileObject{}).Select("id,tenant_id,thumb_attempts").
			Where("id>? AND thumb_status='pending' AND purpose <> 'avatar' AND thumb_attempts<3 AND created_at<?", after, now.Add(-10*time.Minute)).
			Where("NOT EXISTS (SELECT 1 FROM outbox_event e WHERE e.event_type='thumb.generate' AND e.tenant_id=file_object.tenant_id AND e.aggregate_id=file_object.id::text AND e.created_at>=?)", now.UTC().Truncate(time.Hour)).
			Order("id").Limit(100).Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).Find(&rows).Error
		if err != nil {
			return err
		}
		for _, row := range rows {
			if err := q.Model(&model.FileObject{}).Where("tenant_id=? AND id=? AND thumb_status='pending' AND thumb_attempts=?", row.TenantID, row.ID, row.ThumbAttempts).
				Update("thumb_attempts", gorm.Expr("thumb_attempts+1")).Error; err != nil {
				return err
			}
			if err := s.queueThumbnail(tx, row); err != nil {
				return err
			}
			last = row.ID
		}
		return nil
	})
	return last, err
}

func thumbnailSchedule(database *db.DB, next func(context.Context, time.Time) error) func(context.Context, time.Time) error {
	var lastHour time.Time
	return func(ctx context.Context, now time.Time) error {
		err := next(ctx, now)
		hour := now.UTC().Truncate(time.Hour)
		if !hour.After(lastHour) {
			return err
		}
		if sweepErr := sweepThumbnails(ctx, database, now); sweepErr != nil {
			return errors.Join(err, sweepErr)
		}
		lastHour = hour
		return err
	}
}

func sweepThumbnails(ctx context.Context, database *db.DB, now time.Time) error {
	store := newFileStore(database)
	for after := int64(0); ; {
		last, err := store.requeueThumbnails(ctx, now, after)
		if err != nil || last == after {
			return err
		}
		after = last
	}
}

var _ filemodule.ThumbnailStore = (*fileStore)(nil)

func newThumbnailRuntime(database *db.DB, resolver *storage.Resolver, cfg *config.Config, log *slog.Logger) (*worker.QueueServer, error) {
	thumbnails, err := filemodule.NewThumbnailWorker(newFileStore(database), fileResolver(resolver))
	if err != nil {
		return nil, err
	}
	return worker.NewThumbnailQueueServer(cfg.Redis, log, thumbnails.Generate)
}
