// Package storage implements private S3-compatible object storage.
package storage

import (
	"context"
	"errors"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/omwk/go-admin-api/internal/modules/catalog/port"
)

// S3 streams export objects using the pinned MinIO SDK, without a public ACL.
type S3 struct {
	client *minio.Client
	bucket string
}

// NewS3 constructs the export adapter without performing network IO.
func NewS3(cfg config.Storage) (*S3, error) {
	endpoint, secure := strings.TrimSpace(cfg.Endpoint), cfg.UseSSL
	if strings.Contains(endpoint, "://") {
		parsed, err := url.Parse(endpoint)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
			return nil, errors.New("invalid export storage endpoint")
		}
		endpoint, secure = parsed.Host, parsed.Scheme == "https"
	}
	if endpoint == "" || cfg.Bucket == "" {
		return nil, errors.New("export storage endpoint and bucket required")
	}
	client, err := minio.New(endpoint, &minio.Options{Creds: credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""), Secure: secure})
	if err != nil {
		return nil, errors.New("invalid export storage configuration")
	}
	return &S3{client: client, bucket: cfg.Bucket}, nil
}

// Put uploads a single attempt directly; unknown-size input uses bounded multipart IO.
func (s *S3) Put(ctx context.Context, key string, body io.Reader, size int64) (int64, error) {
	info, err := s.client.PutObject(ctx, s.bucket, key, body, size, minio.PutObjectOptions{
		ContentType: "text/csv; charset=utf-8", PartSize: 5 << 20, NumThreads: 1,
	})
	if err != nil {
		return 0, errors.New("export object upload failed")
	}
	return info.Size, nil
}

// Get opens a private object and exposes its verified content length.
func (s *S3) Get(ctx context.Context, key string) (port.Object, error) {
	object, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return port.Object{}, errors.New("export object unavailable")
	}
	info, err := object.Stat()
	if err != nil {
		return port.Object{}, errors.Join(errors.New("export object unavailable"), object.Close())
	}
	return port.Object{Body: object, Size: info.Size}, nil
}

// Delete removes only the exact attempt object supplied by the collector.
func (s *S3) Delete(ctx context.Context, key string) error {
	if err := s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{}); err != nil {
		return errors.New("export object delete failed")
	}
	return nil
}

// PresignGet signs an exact private key for at most one hour.
func (s *S3) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	if ttl < time.Second || ttl > time.Hour {
		return "", errors.New("invalid export download lifetime")
	}
	signed, err := s.client.PresignedGetObject(ctx, s.bucket, key, ttl, nil)
	if err != nil {
		return "", errors.New("export download signing failed")
	}
	return signed.String(), nil
}

// Scan enumerates object metadata, cancelling the SDK producer on callback failure.
func (s *S3) Scan(ctx context.Context, prefix string, visit func(port.ObjectInfo) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	for object := range s.client.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
		if object.Err != nil {
			return errors.New("export object scan failed")
		}
		if err := visit(port.ObjectInfo{Key: object.Key, ModifiedAt: object.LastModified}); err != nil {
			return err
		}
	}
	return ctx.Err()
}

var _ port.ObjectStore = (*S3)(nil)
var _ port.ObjectScanner = (*S3)(nil)
