package shop

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/idem"
	"github.com/omwk/go-admin-api/internal/modules/shop/model"
	"github.com/stretchr/testify/require"
)

func TestFileUploadRejectsOversizeAndDeletesStaging(t *testing.T) {
	service, objects, _ := newFileTestService(t)
	data := append(jpegHeader(), bytes.Repeat([]byte("x"), int(MaxFileSize))...)
	_, _, err := service.Upload(context.Background(), FileInput{
		TenantID: 1, UserID: 2, OriginalName: "large.jpg", DeclaredMIME: "image/jpeg", Reader: bytes.NewReader(data),
	})
	require.Error(t, err)
	app := errs.From(err)
	require.Equal(t, 413, app.HTTPStatus)
	require.Empty(t, objects.keys())
}

func TestFileUploadMapsBodyLimitErrorTo413(t *testing.T) {
	service, _ := newFileTestServiceWithObject(t, &maxBytesObjectStore{})
	_, _, err := service.Upload(context.Background(), FileInput{
		TenantID: 1, UserID: 2, OriginalName: "large.jpg", Reader: bytes.NewReader(jpegHeader()),
	})
	require.Error(t, err)
	require.Equal(t, http.StatusRequestEntityTooLarge, errs.From(err).HTTPStatus)
}

func TestFileUploadRejectsZipWithPNGExtensionWithoutObject(t *testing.T) {
	service, objects, _ := newFileTestService(t)
	_, _, err := service.Upload(context.Background(), FileInput{
		TenantID: 1, UserID: 2, OriginalName: "image.png", DeclaredMIME: "image/png", Reader: bytes.NewReader([]byte("PK\x03\x04zip")),
	})
	require.Error(t, err)
	require.Equal(t, 400, errs.From(err).HTTPStatus)
	require.Empty(t, objects.keys())
}

func TestFileUploadDeduplicatesSameTenantContent(t *testing.T) {
	service, objects, store := newFileTestService(t)
	data := append(jpegHeader(), []byte("same-content")...)
	first, _, err := service.Upload(context.Background(), FileInput{TenantID: 1, UserID: 2, OriginalName: "one.jpg", Reader: bytes.NewReader(data)})
	require.NoError(t, err)
	require.NotEmpty(t, first.CreatedAt)
	second, _, err := service.Upload(context.Background(), FileInput{TenantID: 1, UserID: 2, OriginalName: "two.jpg", Reader: bytes.NewReader(data)})
	require.NoError(t, err)
	require.Equal(t, first.ID, second.ID)
	require.Len(t, store.rows, 1)
	require.Len(t, objects.keys(), 1)
}

func TestFileUploadSameContentAcrossUsersDoesNotLeaveUnreferencedObject(t *testing.T) {
	service, objects, store := newFileTestService(t)
	data := append(jpegHeader(), []byte("shared-content")...)
	first, _, err := service.Upload(context.Background(), FileInput{TenantID: 1, UserID: 2, OriginalName: "one.jpg", Reader: bytes.NewReader(data)})
	require.NoError(t, err)
	second, _, err := service.Upload(context.Background(), FileInput{TenantID: 1, UserID: 3, OriginalName: "two.jpg", Reader: bytes.NewReader(data)})
	require.NoError(t, err)
	require.Equal(t, first.ID, second.ID)
	require.Len(t, store.rows, 1)
	require.Len(t, objects.keys(), 1)
}

func TestFileUploadIdempotencyMismatchReturns10006AndCleansNewObject(t *testing.T) {
	service, objects, _ := newFileTestService(t)
	key := "upload-key"
	_, _, err := service.Upload(context.Background(), FileInput{TenantID: 1, UserID: 2, OriginalName: "one.jpg", Reader: bytes.NewReader(append(jpegHeader(), []byte("one")...)), IdempotencyKey: key})
	require.NoError(t, err)
	_, _, err = service.Upload(context.Background(), FileInput{TenantID: 1, UserID: 2, OriginalName: "one.jpg", Reader: bytes.NewReader(append(jpegHeader(), []byte("two")...)), IdempotencyKey: key})
	require.Error(t, err)
	require.Equal(t, errs.ErrIdempotencyMismatch, errs.From(err).Code)
	require.Len(t, objects.keys(), 1)
}

func TestFileGetCrossTenantReturnsNotFound(t *testing.T) {
	service, _, _ := newFileTestService(t)
	view, _, err := service.Upload(context.Background(), FileInput{
		TenantID: 1, UserID: 2, OriginalName: "image.jpg", Reader: bytes.NewReader(append(jpegHeader(), []byte("tenant-owned")...)),
	})
	require.NoError(t, err)

	_, body, _, _, err := service.Get(context.Background(), 2, view.ID)
	require.Error(t, err)
	require.Nil(t, body)
	require.Equal(t, errs.ErrNotFound, errs.From(err).Code)
}

func TestFileUploadSanitizesWindowsPathInOriginalName(t *testing.T) {
	service, _, _ := newFileTestService(t)
	view, _, err := service.Upload(context.Background(), FileInput{
		TenantID: 1, UserID: 2, OriginalName: "..\\image.JPG", Reader: bytes.NewReader(append(jpegHeader(), []byte("path-safe")...)),
	})
	require.NoError(t, err)
	require.Equal(t, "image.JPG", view.OriginalName)
}

func BenchmarkFileUploadStreaming(b *testing.B) {
	data := append(jpegHeader(), bytes.Repeat([]byte("x"), 8<<20)...)
	service, _ := newFileTestServiceWithObject(b, &discardObjectStore{})
	b.ReportAllocs()
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, err := service.Upload(context.Background(), FileInput{TenantID: 1, UserID: 2, OriginalName: "bench.jpg", Reader: bytes.NewReader(data)})
		if err != nil {
			b.Fatal(err)
		}
	}
}

func jpegHeader() []byte { return []byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0} }

type memoryFileStore struct {
	mu   sync.Mutex
	next int64
	rows map[int64]model.FileObject
}

func (s *memoryFileStore) FileTransaction(_ context.Context, fn func(context.Context) error) error {
	return fn(context.Background())
}
func (s *memoryFileStore) LockFileHash(context.Context, int64, string) error { return nil }
func (s *memoryFileStore) FindFileByHash(_ context.Context, tenantID int64, hash string) (model.FileObject, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, row := range s.rows {
		if row.TenantID == tenantID && row.SHA256 == hash {
			return row, nil
		}
	}
	return model.FileObject{}, ErrFileNotFound
}
func (s *memoryFileStore) CreateFile(_ context.Context, row *model.FileObject) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.next++
	row.ID = s.next
	s.rows[row.ID] = *row
	return nil
}
func (s *memoryFileStore) File(_ context.Context, tenantID, id int64) (model.FileObject, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rows[id]
	if !ok || row.TenantID != tenantID {
		return model.FileObject{}, ErrFileNotFound
	}
	return row, nil
}

type memoryObjectStore struct {
	mu      sync.Mutex
	objects map[string][]byte
}

func (s *memoryObjectStore) Put(_ context.Context, key, _ string, body io.Reader) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.objects[key]; ok {
		return false, nil
	}
	data, err := io.ReadAll(body)
	if err != nil {
		return false, err
	}
	s.objects[key] = data
	return true, nil
}
func (s *memoryObjectStore) Copy(_ context.Context, source, destination, _ string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.objects[destination]; ok {
		return false, nil
	}
	data, ok := s.objects[source]
	if !ok {
		return false, errors.New("source object missing")
	}
	s.objects[destination] = append([]byte(nil), data...)
	return true, nil
}
func (s *memoryObjectStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.objects, key)
	return nil
}
func (s *memoryObjectStore) Open(_ context.Context, key string) (io.ReadCloser, string, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.objects[key]
	if !ok {
		return nil, "", 0, errors.New("object missing")
	}
	return io.NopCloser(bytes.NewReader(data)), "image/jpeg", int64(len(data)), nil
}
func (s *memoryObjectStore) keys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := make([]string, 0, len(s.objects))
	for key := range s.objects {
		keys = append(keys, key)
	}
	return keys
}

type memoryIdempotencyStore struct {
	mu   sync.Mutex
	rows map[string]struct {
		hash   string
		result idem.Result
		done   bool
	}
}

func (s *memoryIdempotencyStore) Claim(_ context.Context, op idem.Operation) (idem.Claim, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := op.Scope + ":" + op.KeyDigest
	row, ok := s.rows[key]
	if !ok {
		s.rows[key] = struct {
			hash   string
			result idem.Result
			done   bool
		}{hash: op.RequestHash}
		return idem.Claim{Action: idem.Execute}, nil
	}
	if row.hash != op.RequestHash {
		return idem.Claim{}, idem.ErrMismatch
	}
	if !row.done {
		return idem.Claim{}, idem.ErrProcessing
	}
	return idem.Claim{Action: idem.Replay, Result: row.result}, nil
}
func (s *memoryIdempotencyStore) Complete(_ context.Context, op idem.Operation, result idem.Result) (idem.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := op.Scope + ":" + op.KeyDigest
	row := s.rows[key]
	row.result, row.done = result, true
	s.rows[key] = row
	return result, nil
}

type discardObjectStore struct{}

type maxBytesObjectStore struct{}

func (s *maxBytesObjectStore) Put(context.Context, string, string, io.Reader) (bool, error) {
	return false, &http.MaxBytesError{Limit: MaxFileSize}
}
func (s *maxBytesObjectStore) Copy(context.Context, string, string, string) (bool, error) {
	return false, errors.New("copy should not be called")
}
func (s *maxBytesObjectStore) Delete(context.Context, string) error { return nil }
func (s *maxBytesObjectStore) Open(context.Context, string) (io.ReadCloser, string, int64, error) {
	return nil, "", 0, errors.New("open should not be called")
}

func (s *discardObjectStore) Put(_ context.Context, _ string, _ string, body io.Reader) (bool, error) {
	_, err := io.Copy(io.Discard, body)
	return true, err
}
func (s *discardObjectStore) Copy(context.Context, string, string, string) (bool, error) {
	return true, nil
}
func (s *discardObjectStore) Delete(context.Context, string) error { return nil }
func (s *discardObjectStore) Open(context.Context, string) (io.ReadCloser, string, int64, error) {
	return io.NopCloser(bytes.NewReader(nil)), "image/jpeg", 0, nil
}

func newFileTestService(t *testing.T) (*FileService, *memoryObjectStore, *memoryFileStore) {
	objects := &memoryObjectStore{objects: map[string][]byte{}}
	service, store := newFileTestServiceWithObject(t, objects)
	return service, objects, store
}

func newFileTestServiceWithObject(t testing.TB, object ObjectStore) (*FileService, *memoryFileStore) {
	store := &memoryFileStore{rows: map[int64]model.FileObject{}}
	idemStore := &memoryIdempotencyStore{rows: map[string]struct {
		hash   string
		result idem.Result
		done   bool
	}{}}
	service, err := NewFileService(store, object, idemStore, "test-bucket")
	require.NoError(t, err)
	return service, store
}
