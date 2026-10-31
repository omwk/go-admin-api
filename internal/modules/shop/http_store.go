package shop

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
)

// HTTPObjectStore is a small HTTP object-store adapter for S3/MinIO-compatible gateways.
// Deployments may place a signing proxy in front of the endpoint; this adapter keeps
// request bodies streamed and never buffers object content.
type HTTPObjectStore struct {
	endpoint string
	bucket   string
	client   *http.Client
	access   string
	secret   string
}

// NewHTTPObjectStore constructs an object store from configured storage settings.
func NewHTTPObjectStore(endpoint, bucket, accessKey, secretKey string) *HTTPObjectStore {
	return &HTTPObjectStore{endpoint: strings.TrimRight(endpoint, "/"), bucket: bucket, access: accessKey, secret: secretKey, client: &http.Client{Timeout: 0}}
}

func (s *HTTPObjectStore) Put(ctx context.Context, key, contentType string, body io.Reader) (bool, error) {
	if s.endpoint == "" || s.bucket == "" {
		return false, errors.New("object storage is not configured")
	}
	if exists, err := s.exists(ctx, key); err != nil {
		return false, err
	} else if exists {
		return false, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, s.objectURL(key), body)
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", contentType)
	s.auth(req)
	resp, err := s.client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, errors.New("object storage rejected upload")
	}
	return true, nil
}

func (s *HTTPObjectStore) Copy(ctx context.Context, source, destination, contentType string) (bool, error) {
	if exists, err := s.exists(ctx, destination); err != nil {
		return false, err
	} else if exists {
		return false, nil
	}
	body, _, _, err := s.Open(ctx, source)
	if err != nil {
		return false, err
	}
	defer body.Close()
	return s.Put(ctx, destination, contentType, body)
}

func (s *HTTPObjectStore) Delete(ctx context.Context, key string) error {
	if s.endpoint == "" || s.bucket == "" {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, s.objectURL(key), nil)
	if err != nil {
		return err
	}
	s.auth(req)
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound && (resp.StatusCode < 200 || resp.StatusCode >= 300) {
		return errors.New("object storage rejected delete")
	}
	return nil
}

func (s *HTTPObjectStore) Open(ctx context.Context, key string) (io.ReadCloser, string, int64, error) {
	if s.endpoint == "" || s.bucket == "" {
		return nil, "", 0, errors.New("object storage is not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.objectURL(key), nil)
	if err != nil {
		return nil, "", 0, err
	}
	s.auth(req)
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, "", 0, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()
		return nil, "", 0, errors.New("object storage object is unavailable")
	}
	return resp.Body, resp.Header.Get("Content-Type"), resp.ContentLength, nil
}

func (s *HTTPObjectStore) exists(ctx context.Context, key string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, s.objectURL(key), nil)
	if err != nil {
		return false, err
	}
	s.auth(req)
	resp, err := s.client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, errors.New("object storage existence check failed")
	}
	return true, nil
}

func (s *HTTPObjectStore) objectURL(key string) string {
	return s.endpoint + "/" + path.Join(url.PathEscape(s.bucket), url.PathEscape(key))
}

func (s *HTTPObjectStore) auth(req *http.Request) {
	if s.access != "" {
		req.SetBasicAuth(s.access, s.secret)
	}
}
