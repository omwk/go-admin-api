package system

import (
	"context"
	"time"

	"github.com/omwk/go-admin-api/internal/infra/storage"
)

// StorageTestInput contains only connection settings; no saved provider is required.
type StorageTestInput struct {
	Endpoint  string `json:"endpoint"`
	Port      *int   `json:"port"`
	Bucket    string `json:"bucket"`
	UseSSL    *bool  `json:"use_ssl"`
	PathStyle *bool  `json:"path_style"`
	Region    string `json:"region"`
	AccessKey string `json:"access_key"`
	SecretKey string `json:"secret_key"`
}

func (in StorageTestInput) configuration() StorageInput {
	return StorageInput{Name: "connection-test", Endpoint: in.Endpoint, Port: in.Port, Bucket: in.Bucket, UseSSL: in.UseSSL, PathStyle: in.PathStyle, Region: in.Region, AccessKey: in.AccessKey, SecretKey: in.SecretKey}
}

func (s *StorageService) probeStorageInput(ctx context.Context, a Actor, in StorageInput) (storage.Provider, string, error) {
	if a.TenantID != 1 || a.UserID <= 0 {
		return storage.Provider{}, "", notFound()
	}
	p, err := in.provider()
	if err != nil {
		return storage.Provider{}, "", err
	}
	return probeProvider(ctx, p)
}

func probeProvider(ctx context.Context, p storage.Provider) (storage.Provider, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	object, err := storage.NewProviderS3(p)
	if err != nil {
		return p, "", err
	}
	return p, object.Test(ctx), nil
}
