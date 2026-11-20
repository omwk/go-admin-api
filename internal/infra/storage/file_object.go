package storage

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/minio/minio-go/v7"
)

// FileObject adapts SDK operations to the streaming file module port.
type FileObject struct{ S3 *S3 }

// WithBucket retains the upload-time bucket for historical reads.
func (s *S3) WithBucket(bucket string) *S3 {
	if bucket == "" {
		return s
	}
	clone := *s
	clone.bucket = bucket
	return &clone
}

// Put uploads a fresh staging key through the SDK.
func (f FileObject) Put(ctx context.Context, key, mime string, r io.Reader) (bool, error) {
	_, err := f.S3.PutFile(ctx, key, mime, r, -1)
	return err == nil, err
}

// Copy places validated staging bytes at their final private key.
func (f FileObject) Copy(ctx context.Context, source, destination, mime string) (bool, error) {
	_, err := f.S3.client.CopyObject(ctx, minio.CopyDestOptions{Bucket: f.S3.bucket, Object: destination, ReplaceMetadata: true, UserMetadata: map[string]string{"Content-Type": mime}}, minio.CopySrcOptions{Bucket: f.S3.bucket, Object: source})
	if err != nil {
		return false, errors.New("object copy failed")
	}
	return true, nil
}

// Delete removes the exact key.
func (f FileObject) Delete(ctx context.Context, key string) error { return f.S3.Delete(ctx, key) }

// Open verifies object metadata before returning its stream.
func (f FileObject) Open(ctx context.Context, key string) (io.ReadCloser, string, int64, error) {
	o, err := f.S3.Get(ctx, key)
	return o.Body, "", o.Size, err
}

// PresignGet returns an SDK-signed five-minute download URL.
func (f FileObject) PresignGet(ctx context.Context, key string) (string, error) {
	return f.S3.PresignGet(ctx, key, 5*time.Minute)
}
