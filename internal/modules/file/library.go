// Package file implements tenant-scoped object uploads and controlled downloads.
package file

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/kernel/idem"
	"github.com/omwk/go-admin-api/internal/modules/file/model"
	"github.com/omwk/go-admin-api/internal/modules/file/repo"
)

// Binding pins storage throughout an upload and its cleanup.
type Binding struct {
	Object     ObjectStore
	Bucket     string
	ProviderID *int64
}

// Resolver reads the default or the recorded historical configuration.
type Resolver func(context.Context, *int64, string) (Binding, error)

// Recorder persists sanitized audit records.
type Recorder func(context.Context, audit.Event) error

// LibraryStore adds scoped file management to metadata persistence.
type LibraryStore interface {
	repo.FileStore
	ListFiles(context.Context, int64, string, string, int, int) ([]model.FileObject, int64, error)
	DeleteFile(context.Context, int64, int64) error
	QueueFileCleanup(context.Context, model.FileObject) error
}

// Page is a bounded metadata result page.
type Page struct {
	List     []View `json:"list"`
	Total    int64  `json:"total"`
	Page     int    `json:"page"`
	PageSize int    `json:"page_size"`
}

// NewResolvedFileService requires live storage resolution and durable auditing.
func NewResolvedFileService(store repo.FileStore, resolver Resolver, idempotency idem.Store, record Recorder) (*Service, error) {
	if store == nil || resolver == nil || record == nil {
		return nil, errors.New("file dependencies required")
	}
	return &Service{store: store, resolver: resolver, idem: idempotency, record: record, now: time.Now}, nil
}
func (s *Service) bound(ctx context.Context, id *int64, bucket string) (Service, error) {
	clone := *s
	if s.resolver != nil {
		b, err := s.resolver(ctx, id, bucket)
		if err != nil {
			return clone, err
		}
		clone.object, clone.bucket, clone.providerID = b.Object, b.Bucket, b.ProviderID
	}
	if clone.object == nil {
		return clone, errors.New("file storage unavailable")
	}
	return clone, nil
}

// List applies tenant identity and bounded metadata filters.
func (s *Service) List(ctx context.Context, tenant int64, purpose, keyword string, page, size int) (Page, error) {
	result := Page{List: []View{}, Page: page, PageSize: size}
	if tenant <= 0 || page < 1 || page > 10000 || size < 1 || size > 100 || len(keyword) > 256 || (purpose != "" && !validPurpose(purpose)) {
		return result, invalidFile()
	}
	store, ok := s.store.(LibraryStore)
	if !ok {
		return result, errors.New("file library persistence unavailable")
	}
	rows, total, err := store.ListFiles(ctx, tenant, purpose, keyword, page, size)
	if err != nil {
		return result, err
	}
	result.Total = total
	for _, row := range rows {
		result.List = append(result.List, projectFile(row))
	}
	return result, nil
}

// Metadata returns safe same-tenant metadata without opening object storage.
func (s *Service) Metadata(ctx context.Context, tenant, id int64) (View, error) {
	if tenant <= 0 || id <= 0 {
		return View{}, invalidFile()
	}
	row, err := s.store.File(ctx, tenant, id)
	if err != nil {
		return View{}, err
	}
	return projectFile(row), nil
}

// URL signs the recorded provider and bucket for five minutes.
func (s *Service) URL(ctx context.Context, tenant, id int64) (string, error) {
	row, err := s.store.File(ctx, tenant, id)
	if err != nil {
		return "", err
	}
	bound, err := s.bound(ctx, row.ProviderID, row.Bucket)
	if err != nil {
		return "", err
	}
	signer, ok := bound.object.(interface {
		PresignGet(context.Context, string) (string, error)
	})
	if !ok {
		return "", errors.New("file signing unavailable")
	}
	return signer.PresignGet(ctx, row.ObjectKey)
}

// Delete removes scoped metadata and durably schedules object cleanup after commit.
func (s *Service) Delete(ctx context.Context, tenant, user, id int64) error {
	row, err := s.store.File(ctx, tenant, id)
	if err != nil {
		return err
	}
	bound, err := s.bound(ctx, row.ProviderID, row.Bucket)
	if err != nil {
		return err
	}
	row.ProviderID = bound.providerID
	if row.Bucket == "" {
		row.Bucket = bound.bucket
	}
	store, ok := s.store.(LibraryStore)
	if !ok {
		return errors.New("file library persistence unavailable")
	}
	return s.store.FileTransaction(ctx, func(tx context.Context) error {
		if err := store.DeleteFile(tx, tenant, id); err != nil {
			return err
		}
		if s.record != nil {
			if err := s.record(tx, audit.Event{TenantID: tenant, ActorType: "user", ActorID: &user, Action: "file.delete", ResourceType: "file", ResourceID: strconv.FormatInt(id, 10), Before: map[string]any{"original_name": row.OriginalName, "sha256": row.SHA256}, Result: "success"}); err != nil {
				return err
			}
		}
		return store.QueueFileCleanup(tx, row)
	})
}
func (s *Service) recordUpload(ctx context.Context, input Input, row model.FileObject) error {
	return s.record(ctx, audit.Event{TenantID: input.TenantID, ActorType: "user", ActorID: &input.UserID, Action: "file.upload", ResourceType: "file", ResourceID: strconv.FormatInt(row.ID, 10), After: map[string]any{"original_name": row.OriginalName, "sha256": row.SHA256, "purpose": row.Purpose, "size_bytes": row.SizeBytes}, Result: "success"})
}
func validPurpose(p string) bool {
	return p == "attachment" || p == "shop" || p == "export" || p == "snapshot"
}
func safeObjectName(name string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '_' || r == '-' {
			return r
		}
		return '_'
	}, name)
}
