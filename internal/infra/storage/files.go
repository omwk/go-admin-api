package storage

import (
	"context"
	"errors"
	"io"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// NewProviderS3 constructs the sole SDK adapter from a database provider.
func NewProviderS3(p Provider) (*S3, error) {
	endpoint, secure, err := NormalizeEndpoint(p.Endpoint, p.Port, p.UseSSL)
	if err != nil || p.Bucket == "" {
		return nil, errors.New("invalid storage configuration")
	}
	lookup := minio.BucketLookupPath
	if !p.PathStyle {
		// Auto recognizes AWS hosts only without an explicit default port.
		if secure {
			endpoint = strings.TrimSuffix(endpoint, ":443")
		} else {
			endpoint = strings.TrimSuffix(endpoint, ":80")
		}
		lookup = minio.BucketLookupAuto
	}
	client, err := minio.New(endpoint, &minio.Options{Creds: credentials.NewStaticV4(p.AccessKey, p.SecretKey, ""), Secure: secure, Region: p.Region, BucketLookup: lookup})
	if err != nil {
		return nil, errors.New("invalid storage configuration")
	}
	return &S3{client: client, bucket: p.Bucket}, nil
}

// PutFile streams an object with its validated MIME type.
func (s *S3) PutFile(ctx context.Context, key, mime string, body io.Reader, size int64) (int64, error) {
	info, err := s.client.PutObject(ctx, s.bucket, key, body, size, minio.PutObjectOptions{ContentType: mime, PartSize: 5 << 20, NumThreads: 1})
	if err != nil {
		return 0, errors.New("object upload failed")
	}
	return info.Size, nil
}

// Test checks bucket access and returns a stable result without upstream text.
func (s *S3) Test(ctx context.Context) string {
	exists, err := s.client.BucketExists(ctx, s.bucket)
	if err == nil {
		if exists {
			return "ok"
		}
		return "bucket_missing"
	}
	switch minio.ToErrorResponse(err).Code {
	case "AccessDenied", "InvalidAccessKeyId", "InvalidAccessKeyID", "SignatureDoesNotMatch":
		return "access_denied"
	case "NoSuchBucket":
		return "bucket_missing"
	default:
		return "unreachable"
	}
}
