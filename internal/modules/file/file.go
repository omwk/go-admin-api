package file

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/idem"
	"github.com/omwk/go-admin-api/internal/modules/file/model"
	"github.com/omwk/go-admin-api/internal/modules/file/repo"
)

const (
	// MaxFileSize is the maximum accepted payload size.
	MaxFileSize     int64 = 20 << 20
	filePrefixLimit       = 512
)

// ObjectStore streams objects without exposing storage implementation details.
type ObjectStore interface {
	Put(context.Context, string, string, io.Reader, int64) (bool, error)
	Copy(context.Context, string, string, string) (bool, error)
	Delete(context.Context, string) error
	Open(context.Context, string) (io.ReadCloser, string, int64, error)
}

// Input contains request metadata and the multipart file stream.
type Input struct {
	TenantID       int64
	UserID         int64
	OriginalName   string
	DeclaredMIME   string
	Source         string
	SizeBytes      int64
	OnRegister     func(context.Context) error
	Reader         io.Reader
	IdempotencyKey string
	Serialize      func(int, any) (idem.Result, error)
}

// View is the safe public file projection.
type View struct {
	ID             int64   `json:"id"`
	OriginalName   string  `json:"original_name"`
	MIME           string  `json:"mime"`
	SizeBytes      int64   `json:"size_bytes"`
	SHA256         string  `json:"sha256"`
	Source         string  `json:"source"`
	Category       string  `json:"category"`
	ThumbURL       *string `json:"thumb_url"`
	ThumbStatus    string  `json:"thumb_status"`
	UploadedBy     *int64  `json:"uploaded_by"`
	UploadedByName string  `json:"uploaded_by_name"`
	CreatedAt      string  `json:"created_at"`
}

// Service implements bounded upload, object placement, and scoped reads.
type Service struct {
	resolver       Resolver
	providerID     *int64
	record         Recorder
	store          repo.FileStore
	object         ObjectStore
	idem           idem.Store
	bucket         string
	now            func() time.Time
	accessMode     string
	quotaPerUserMB int64
}

// NewFileService constructs the file service with injected persistence and object storage.
func NewFileService(store repo.FileStore, object ObjectStore, idempotency idem.Store, bucket ...string) (*Service, error) {
	if store == nil || object == nil {
		return nil, errors.New("file service dependencies are required")
	}
	configuredBucket := ""
	if len(bucket) > 0 {
		configuredBucket = strings.TrimSpace(bucket[0])
	}
	return &Service{store: store, object: object, idem: idempotency, bucket: configuredBucket, now: time.Now}, nil
}

// Get opens a same-tenant object for controlled streaming download.
func (s *Service) Get(ctx context.Context, tenantID, id int64) (model.FileObject, io.ReadCloser, string, int64, error) {
	row, err := s.store.File(ctx, tenantID, id)
	if err != nil {
		return model.FileObject{}, nil, "", 0, err
	}
	bound, err := s.bound(ctx, row.ProviderID, row.Bucket)
	if err != nil {
		return model.FileObject{}, nil, "", 0, err
	}
	body, contentType, size, err := bound.object.Open(ctx, row.ObjectKey)
	if err != nil {
		return model.FileObject{}, nil, "", 0, err
	}
	return row, body, contentType, size, nil
}

func projectFile(row model.FileObject) View {
	name := row.UploadedByName
	if row.UploadedBy != nil && name == "" {
		name = fmt.Sprintf("#%d", *row.UploadedBy)
	}
	category := row.Category
	if category == "" {
		category = Category(row.MIME)
	}
	source := row.Purpose
	if source == "product_image" {
		source = "shop"
	}
	return View{ID: row.ID, OriginalName: row.OriginalName, MIME: row.MIME, SizeBytes: row.SizeBytes, SHA256: row.SHA256, Source: source, Category: category, ThumbStatus: row.ThumbStatus, UploadedBy: row.UploadedBy, UploadedByName: name, CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339Nano)}
}

func randomObjectID() string {
	var entropy [16]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return hex.EncodeToString(entropy[:])
	}
	return hex.EncodeToString(entropy[:])
}

// ErrFileNotFound is returned when file metadata is outside the caller's tenant.
var ErrFileNotFound = errs.New(errs.ErrNotFound, http.StatusNotFound, "资源不存在")

func invalidFile() error {
	return errs.New(errs.ErrInvalidParam, http.StatusBadRequest, "文件参数无效")
}
func tooLargeOrInvalid() error {
	return errs.New(errs.ErrInvalidParam, http.StatusRequestEntityTooLarge, "文件超过 20 MB 上限")
}

func mapFileIdempotencyError(err error) error {
	if errors.Is(err, idem.ErrMismatch) {
		return errs.New(errs.ErrIdempotencyMismatch, http.StatusConflict, "幂等键请求内容不一致")
	}
	if errors.Is(err, idem.ErrProcessing) {
		return errs.New(errs.ErrConflict, http.StatusConflict, "幂等请求正在处理")
	}
	return err
}
