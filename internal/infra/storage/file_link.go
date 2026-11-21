package storage

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"
)

// PublicURL constructs an unsigned address from the configured CDN or exact bucket endpoint.
func (f FileObject) PublicURL(key string) (string, error) {
	u := *f.S3.client.EndpointURL()
	if f.PublicBaseURL != "" {
		parsed, err := url.Parse(f.PublicBaseURL)
		if err != nil || parsed.Host == "" {
			return "", errors.New("invalid public storage URL")
		}
		u = *parsed
	} else {
		u.Path = "/" + f.S3.bucket
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/" + key
	u.RawPath, u.RawQuery, u.Fragment = "", "", ""
	return u.String(), nil
}

// PresignPreview allows a private JPEG thumbnail to render inline for five minutes.
func (f FileObject) PresignPreview(ctx context.Context, key string) (string, error) {
	u, err := f.S3.client.PresignedGetObject(ctx, f.S3.bucket, key, 5*time.Minute, nil)
	if err != nil {
		return "", errors.New("file preview signing failed")
	}
	return u.String(), nil
}
