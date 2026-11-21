package file

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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
)

// Upload validates a bounded stream, checks quota and registers its pinned objects.
func (s *Service) Upload(ctx context.Context, input Input) (view View, result idem.Result, retErr error) {
	if input.TenantID <= 0 || input.UserID <= 0 || input.Reader == nil {
		return view, result, invalidFile()
	}
	bound, err := s.bound(ctx, nil, "")
	if err != nil {
		return view, result, err
	}
	s = &bound
	name := filepath.Base(strings.ReplaceAll(strings.TrimSpace(input.OriginalName), "\\", "/"))
	if name == "." || name == "" {
		return view, result, emptyFilename()
	}
	if len(name) > 512 {
		return view, result, errs.New(errs.ErrInvalidParam, 400, "文件名过长")
	}
	source := strings.TrimSpace(input.Source)
	if source == "" {
		source = "attachment"
	}
	if !validPurpose(source) {
		return view, result, invalidFile()
	}
	if err := s.checkQuota(ctx, input, input.SizeBytes); err != nil {
		return s.quotaPreflight(ctx, input, name, source, err)
	}
	staging := fmt.Sprintf(".staging/%d/%s", input.TenantID, randomObjectID())
	row := model.FileObject{TenantID: input.TenantID, ProviderID: s.providerID, Bucket: s.bucket, OriginalName: name, Purpose: source, UploadedBy: &input.UserID, CreatedAt: s.now().UTC()}
	row.ObjectKey = fmt.Sprintf("tenants/%d/%s/%s/%s-%s", input.TenantID, source, s.now().UTC().Format("2006/01"), randomObjectID(), safeObjectName(name))
	keep := false
	defer func() {
		keys := []string{staging}
		if !keep {
			keys = append(keys, row.ObjectKey, row.ObjectKey+".thumb.jpg")
		}
		if err := s.cleanupObjects(ctx, keys); err != nil {
			retErr = errors.Join(retErr, err)
			view = View{}
			result = idem.Result{}
		}
	}()
	prefix, size, sniffed, err := s.streamToStaging(ctx, input, staging)
	if err != nil {
		return view, result, err
	}
	if err := validateFile(name, input.DeclaredMIME, sniffed, size, prefix.data); err != nil {
		return view, result, err
	}
	row.SHA256, row.MIME, row.SizeBytes, row.Category = hex.EncodeToString(prefix.digest[:]), sniffed, size, Category(sniffed)
	if err := s.checkQuota(ctx, input, size); err != nil {
		return s.quotaReplay(ctx, input, row, err)
	}
	if created, err := s.object.Copy(ctx, staging, row.ObjectKey, sniffed); err != nil || !created {
		if err == nil {
			err = errors.New("file object already exists")
		}
		return view, result, err
	}
	if len(prefix.image) > 0 {
		row.ThumbKey, err = s.thumbnail(ctx, row.ObjectKey, prefix.image)
		if err != nil {
			return view, result, err
		}
	}
	operation, err := s.operation(input, name, source, sniffed, size, row.SHA256)
	if err != nil {
		return view, result, err
	}
	view, result, keep, err = s.register(ctx, input, operation, row)
	return view, result, err
}

func (s *Service) cleanupObjects(ctx context.Context, keys []string) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	var result error
	for _, key := range keys {
		if err := s.object.Delete(ctx, key); err != nil {
			result = errors.Join(result, fmt.Errorf("cleanup file object: %w", err))
		}
	}
	return result
}

type streamResult struct {
	data   []byte
	digest [sha256.Size]byte
	image  []byte
}

func (s *Service) streamToStaging(ctx context.Context, input Input, staging string) (streamResult, int64, string, error) {
	return streamUpload(input, func(mime string, reader io.Reader) (bool, error) {
		return s.object.Put(ctx, staging, mime, reader)
	})
}

func streamUpload(input Input, put func(string, io.Reader) (bool, error)) (streamResult, int64, string, error) {
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
	counted := &digestReader{reader: io.MultiReader(bytes.NewReader(prefix), limited), hash: hash, capture: sniffed == "image/jpeg" || sniffed == "image/png" || sniffed == "image/gif"}
	reader := io.Reader(counted)
	created, err := put(sniffed, reader)
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
	var data []byte
	if size <= imageInputLimit {
		data = counted.image.Bytes()
	}
	return streamResult{data: prefix, digest: digest, image: data}, size, sniffed, nil
}

type digestReader struct {
	reader  io.Reader
	hash    hash.Hash
	n       int64
	capture bool
	image   bytes.Buffer
}

func (r *digestReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 {
		if _, writeErr := r.hash.Write(p[:n]); writeErr != nil {
			return n, writeErr
		}
		r.n += int64(n)
		if r.capture && r.image.Len() < imageInputLimit {
			if _, err := r.image.Write(p[:min(n, imageInputLimit-r.image.Len())]); err != nil {
				return n, err
			}
		}
	}
	return n, err
}
