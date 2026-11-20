package file

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"path/filepath"
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
	Put(context.Context, string, string, io.Reader) (bool, error)
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
	Purpose        string
	Reader         io.Reader
	IdempotencyKey string
	Serialize      func(int, any) (idem.Result, error)
}

// View is the safe public file projection.
type View struct {
	ID           int64  `json:"id"`
	OriginalName string `json:"original_name"`
	MIME         string `json:"mime"`
	SizeBytes    int64  `json:"size_bytes"`
	SHA256       string `json:"sha256"`
	Purpose      string `json:"purpose"`
	CreatedAt    string `json:"created_at"`
}

// Service implements bounded upload, object placement, and scoped reads.
type Service struct {
	resolver   Resolver
	providerID *int64
	record     Recorder
	store      repo.FileStore
	object     ObjectStore
	idem       idem.Store
	bucket     string
	now        func() time.Time
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

// Upload validates and stores a multipart stream without buffering the payload in memory.
func (s *Service) Upload(ctx context.Context, input Input) (view View, result idem.Result, retErr error) {
	if input.TenantID <= 0 || input.UserID <= 0 || input.Reader == nil {
		return View{}, idem.Result{}, invalidFile()
	}
	bound, err := s.bound(ctx, nil, "")
	if err != nil {
		return View{}, idem.Result{}, err
	}
	s = &bound
	name := filepath.Base(strings.ReplaceAll(strings.TrimSpace(input.OriginalName), "\\", "/"))
	if name == "." || name == "" {
		return View{}, idem.Result{}, emptyFilename()
	}
	if len(name) > 512 {
		return View{}, idem.Result{}, errs.New(errs.ErrInvalidParam, http.StatusBadRequest, "文件名过长")
	}
	purpose := strings.TrimSpace(input.Purpose)
	if purpose == "" {
		purpose = "attachment"
	}
	if purpose != "attachment" && purpose != "shop" && purpose != "export" && purpose != "snapshot" {
		return View{}, idem.Result{}, invalidFile()
	}
	staging := fmt.Sprintf(".staging/%d/%s", input.TenantID, randomObjectID())
	defer func() {
		if err := s.object.Delete(context.WithoutCancel(ctx), staging); err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("cleanup staging object: %w", err))
			view = View{}
			result = idem.Result{}
		}
	}()
	prefix, size, sniffed, err := s.streamToStaging(ctx, input, staging)
	if err != nil {
		return View{}, idem.Result{}, err
	}
	if err := validateFile(name, input.DeclaredMIME, sniffed, size, prefix.data); err != nil {
		return View{}, idem.Result{}, err
	}
	sha := hex.EncodeToString(prefix.digest[:])
	key := fmt.Sprintf("tenants/%d/%s/%s/%s-%s", input.TenantID, purpose, s.now().UTC().Format("2006/01"), randomObjectID(), safeObjectName(name))
	created, err := s.object.Copy(ctx, staging, key, sniffed)
	if err != nil {
		return View{}, idem.Result{}, fmt.Errorf("copy file object: %w", err)
	}
	finalCleanup := created
	defer func() {
		if finalCleanup {
			if err := s.object.Delete(context.WithoutCancel(ctx), key); err != nil {
				retErr = errors.Join(retErr, fmt.Errorf("cleanup final object: %w", err))
				view = View{}
				result = idem.Result{}
			}
		}
	}()
	row := model.FileObject{TenantID: input.TenantID, ProviderID: s.providerID, Bucket: s.bucket, ObjectKey: key, OriginalName: name, MIME: sniffed, SizeBytes: size, SHA256: sha, Purpose: purpose}
	row.UploadedBy = &input.UserID
	row.CreatedAt = s.now().UTC()
	operation, err := s.operation(input, name, purpose, sniffed, size, sha)
	if err != nil {
		return View{}, idem.Result{}, err
	}
	var keepObject bool
	view, result, keepObject, err = s.register(ctx, input, operation, row)
	if err != nil {
		return View{}, idem.Result{}, err
	}
	finalCleanup = created && !keepObject
	return view, result, nil
}

type streamResult struct {
	data   []byte
	digest [sha256.Size]byte
}

func (s *Service) streamToStaging(ctx context.Context, input Input, staging string) (streamResult, int64, string, error) {
	prefix := make([]byte, filePrefixLimit)
	n, err := io.ReadFull(input.Reader, prefix)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return streamResult{}, 0, "", invalidFile()
	}
	prefix = prefix[:n]
	if n == 0 {
		return streamResult{}, 0, "", emptyFile()
	}
	sniffed := fileMIME(input.OriginalName, prefix)
	if _, ok := allowedMIME[sniffed]; !ok {
		return streamResult{}, 0, sniffed, unsupportedFileType(sniffed)
	}
	limited := io.LimitReader(input.Reader, MaxFileSize-int64(n)+1)
	hash := sha256.New()
	counted := &digestReader{reader: io.MultiReader(bytes.NewReader(prefix), limited), hash: hash}
	reader := io.Reader(counted)
	created, err := s.object.Put(ctx, staging, sniffed, reader)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return streamResult{}, 0, sniffed, tooLargeOrInvalid()
		}
		return streamResult{}, 0, sniffed, fmt.Errorf("write file object: %w", err)
	}
	if !created {
		return streamResult{}, 0, sniffed, errors.New("staging object already exists")
	}
	size := counted.n
	if size > MaxFileSize {
		return streamResult{}, size, sniffed, tooLargeOrInvalid()
	}
	var digest [sha256.Size]byte
	copy(digest[:], hash.Sum(nil))
	return streamResult{data: prefix, digest: digest}, size, sniffed, nil
}

type digestReader struct {
	reader io.Reader
	hash   hash.Hash
	n      int64
}

func (r *digestReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 {
		if _, writeErr := r.hash.Write(p[:n]); writeErr != nil {
			return n, writeErr
		}
		r.n += int64(n)
	}
	return n, err
}

func (s *Service) operation(input Input, name, purpose, sniffed string, size int64, sha string) (*idem.Operation, error) {
	if strings.TrimSpace(input.IdempotencyKey) == "" {
		return nil, nil
	}
	body, err := json.Marshal(map[string]any{"declared_mime": input.DeclaredMIME, "filename": name, "mime": sniffed, "purpose": purpose, "sha256": sha, "size_bytes": size})
	if err != nil {
		return nil, err
	}
	op, err := idem.Prepare(input.TenantID, input.UserID, idem.Input{Key: input.IdempotencyKey, Method: http.MethodPost, Route: "/api/v1/admin/files", Body: body})
	if err != nil {
		return nil, mapFileIdempotencyError(err)
	}
	return &op, nil
}

func (s *Service) register(ctx context.Context, input Input, operation *idem.Operation, row model.FileObject) (View, idem.Result, bool, error) {
	var view View
	var result idem.Result
	var keepObject bool
	err := s.store.FileTransaction(ctx, func(tx context.Context) error {
		if operation != nil && s.idem != nil {
			claim, err := s.idem.Claim(tx, *operation)
			if err != nil {
				return mapFileIdempotencyError(err)
			}
			if claim.Action == idem.Replay {
				result = claim.Result
				existing, findErr := s.store.FindFileByHash(tx, row.TenantID, row.SHA256)
				keepObject = findErr == nil && existing.ObjectKey == row.ObjectKey
				return nil
			}
		}
		if locker, ok := s.store.(repo.FileHashLocker); ok {
			if err := locker.LockFileHash(tx, row.TenantID, row.SHA256); err != nil {
				return err
			}
		}
		existing, err := s.store.FindFileByHash(tx, row.TenantID, row.SHA256)
		if err == nil {
			view = projectFile(existing)
			keepObject = existing.ObjectKey == row.ObjectKey
		} else if !errors.Is(err, ErrFileNotFound) {
			return err
		} else {
			if err := s.store.CreateFile(tx, &row); err != nil {
				return err
			}
			view = projectFile(row)
			keepObject = true
			if s.record != nil {
				if err := s.recordUpload(tx, input, row); err != nil {
					return err
				}
			}
		}
		if operation != nil && s.idem != nil {
			var err error
			if input.Serialize != nil {
				result, err = input.Serialize(http.StatusCreated, view)
			} else {
				result, err = idem.JSONResult(http.StatusCreated, view)
			}
			if err != nil {
				return err
			}
			result, err = s.idem.Complete(tx, *operation, result)
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return View{}, idem.Result{}, false, err
	}
	return view, result, keepObject, nil
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
	return View{ID: row.ID, OriginalName: row.OriginalName, MIME: row.MIME, SizeBytes: row.SizeBytes, SHA256: row.SHA256, Purpose: row.Purpose, CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339Nano)}
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
