package system

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/omwk/go-admin-api/internal/infra/storage"
	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/kernel/crypto"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/idem"
)

// StorageStore persists platform provider configuration within service transactions.
type StorageStore interface {
	Transaction(context.Context, func(context.Context) error) error
	LockProviders(context.Context) error
	Providers(context.Context) ([]storage.Provider, error)
	Provider(context.Context, int64) (storage.Provider, error)
	SaveProvider(context.Context, *storage.Provider) error
	ClearDefault(context.Context) error
	ProviderReferenced(context.Context, int64) (bool, error)
	DeleteProvider(context.Context, int64) error
}

// StorageInput is the administrator-controlled provider configuration.
type StorageInput struct {
	Name          string `json:"name"`
	Endpoint      string `json:"endpoint"`
	Port          *int   `json:"port"`
	Bucket        string `json:"bucket"`
	UseSSL        *bool  `json:"use_ssl"`
	PathStyle     *bool  `json:"path_style"`
	Region        string `json:"region"`
	AccessKey     string `json:"access_key"`
	SecretKey     string `json:"secret_key"`
	PublicBaseURL string `json:"public_base_url"`
	IsDefault     bool   `json:"is_default"`
	Enabled       *bool  `json:"enabled"`
}

// StorageService manages encrypted providers and audited default transitions.
type StorageService struct {
	idempotency idem.Store
	store       StorageStore
	sealer      crypto.Sealer
	record      func(context.Context, audit.Event) error
}

// NewStorageService constructs provider management without reading deployment settings.
func NewStorageService(store StorageStore, sealer crypto.Sealer, record func(context.Context, audit.Event) error, idempotency ...idem.Store) (*StorageService, error) {
	if store == nil || record == nil {
		return nil, errors.New("storage service dependencies required")
	}
	s := &StorageService{store: store, sealer: sealer, record: record}
	if len(idempotency) > 0 {
		s.idempotency = idempotency[0]
	}
	return s, nil
}

// List returns decrypted configuration to the already-authorized platform actor.
func (s *StorageService) List(ctx context.Context, a Actor) ([]storage.Provider, error) {
	if a.TenantID != 1 || a.UserID <= 0 {
		return nil, notFound()
	}
	rows, err := s.store.Providers(ctx)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		rows[i].SecretKey, err = storage.OpenSecret(s.sealer, rows[i])
		if err != nil {
			return nil, err
		}
	}
	return rows, nil
}

// Get returns a single decrypted provider.
func (s *StorageService) Get(ctx context.Context, a Actor, id int64) (storage.Provider, error) {
	if a.TenantID != 1 || a.UserID <= 0 || id <= 0 {
		return storage.Provider{}, notFound()
	}
	p, err := s.store.Provider(ctx, id)
	if err != nil {
		return p, err
	}
	p.SecretKey, err = storage.OpenSecret(s.sealer, p)
	return p, err
}

func (in StorageInput) provider() (storage.Provider, error) {
	p := storage.Provider{TenantID: 1, Name: strings.TrimSpace(in.Name), Endpoint: strings.TrimSpace(in.Endpoint), Port: 443, Bucket: strings.TrimSpace(in.Bucket), UseSSL: true, PathStyle: true, Region: strings.TrimSpace(in.Region), AccessKey: in.AccessKey, SecretKey: in.SecretKey, PublicBaseURL: strings.TrimSpace(in.PublicBaseURL), IsDefault: in.IsDefault, Enabled: true}
	if in.Port != nil {
		p.Port = *in.Port
	}
	if in.UseSSL != nil {
		p.UseSSL = *in.UseSSL
	}
	if in.PathStyle != nil {
		p.PathStyle = *in.PathStyle
	}
	if in.Enabled != nil {
		p.Enabled = *in.Enabled
	}
	if p.Region == "" {
		p.Region = "us-east-1"
	}
	if p.Name == "" || len(p.Name) > 128 || p.Bucket == "" || p.AccessKey == "" || p.SecretKey == "" || len(p.SecretKey) > 4096 || len(p.Endpoint) > 512 {
		return p, invalidHTTP()
	}
	if p.IsDefault && !p.Enabled {
		return p, errs.New(errs.ErrConflict, 409, "默认配置必须启用")
	}
	if _, _, err := storage.NormalizeEndpoint(p.Endpoint, p.Port, p.UseSSL); err != nil {
		return p, invalidHTTP()
	}
	if p.PublicBaseURL != "" {
		u, err := url.Parse(p.PublicBaseURL)
		if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") || u.RawQuery != "" || u.Fragment != "" {
			return p, invalidHTTP()
		}
	}
	return p, nil
}

// Save creates or replaces one encrypted configuration atomically with its audit record.
func (s *StorageService) Save(ctx context.Context, a Actor, id int64, in StorageInput) (storage.Provider, error) {
	if a.TenantID != 1 || a.UserID <= 0 {
		return storage.Provider{}, notFound()
	}
	p, err := in.provider()
	if err != nil {
		return p, err
	}
	err = s.store.Transaction(ctx, func(tx context.Context) error {
		if err := s.store.LockProviders(tx); err != nil {
			return err
		}
		action := "storage.provider.create"
		if id > 0 {
			old, err := s.store.Provider(tx, id)
			if err != nil {
				return err
			}
			p.ID, p.CreatedAt = old.ID, old.CreatedAt
			action = "storage.provider.update"
		}
		if p.IsDefault {
			if err := s.store.ClearDefault(tx); err != nil {
				return err
			}
		}
		if p.ID == 0 {
			p.SecretEnc = []byte{}
			if err := s.store.SaveProvider(tx, &p); err != nil {
				return err
			}
		}
		p.SecretEnc, err = storage.SealSecret(s.sealer, p)
		if err != nil {
			return err
		}
		if err := s.store.SaveProvider(tx, &p); err != nil {
			return err
		}
		return s.recordProvider(tx, a, action, p)
	})
	return p, err
}

// SetDefault switches the platform default under the same lock as all provider writes.
func (s *StorageService) SetDefault(ctx context.Context, a Actor, id int64) (storage.Provider, error) {
	var p storage.Provider
	if a.TenantID != 1 || a.UserID <= 0 {
		return p, notFound()
	}
	err := s.store.Transaction(ctx, func(tx context.Context) error {
		if err := s.store.LockProviders(tx); err != nil {
			return err
		}
		var err error
		p, err = s.Get(tx, a, id)
		if err != nil {
			return err
		}
		if !p.Enabled {
			return errs.New(errs.ErrConflict, 409, "配置已停用")
		}
		if err := s.store.ClearDefault(tx); err != nil {
			return err
		}
		p.IsDefault = true
		if err := s.store.SaveProvider(tx, &p); err != nil {
			return err
		}
		return s.recordProvider(tx, a, "storage.provider.update", p)
	})
	return p, err
}

// Delete protects default and referenced configurations before deleting.
func (s *StorageService) Delete(ctx context.Context, a Actor, id int64) error {
	if a.TenantID != 1 || a.UserID <= 0 {
		return notFound()
	}
	return s.store.Transaction(ctx, func(tx context.Context) error {
		if err := s.store.LockProviders(tx); err != nil {
			return err
		}
		p, err := s.store.Provider(tx, id)
		if err != nil {
			return err
		}
		if p.IsDefault {
			return errs.New(errs.ErrConflict, 409, "默认配置不可删除")
		}
		used, err := s.store.ProviderReferenced(tx, id)
		if err != nil {
			return err
		}
		if used {
			return errs.New(errs.ErrConflict, 409, "存储配置仍被文件引用")
		}
		if err := s.store.DeleteProvider(tx, id); err != nil {
			return err
		}
		return s.recordProvider(tx, a, "storage.provider.delete", p)
	})
}

// Test reports only stable connection results and persists a sanitized audit.
func (s *StorageService) Test(ctx context.Context, a Actor, id int64) (string, error) {
	p, result, err := s.probeStorage(ctx, a, id)
	if err != nil {
		return "", err
	}
	return result, s.recordProvider(ctx, a, "storage.provider.test", p)
}

func (s *StorageService) probeStorage(ctx context.Context, a Actor, id int64) (storage.Provider, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	p, err := s.Get(ctx, a, id)
	if err != nil {
		return p, "", err
	}
	object, err := storage.NewProviderS3(p)
	if err != nil {
		return p, "", err
	}
	return p, object.Test(ctx), nil
}

func (s *StorageService) recordProvider(ctx context.Context, a Actor, action string, p storage.Provider) error {
	state := map[string]any{"name": p.Name, "endpoint": p.Endpoint, "port": p.Port, "bucket": p.Bucket, "use_ssl": p.UseSSL, "path_style": p.PathStyle, "region": p.Region, "public_base_url": p.PublicBaseURL, "enabled": p.Enabled, "is_default": p.IsDefault, "access_key": "***", "secret_key": "***", "created_at": p.CreatedAt, "updated_at": p.UpdatedAt}
	event := audit.Event{TenantID: a.TenantID, ActorType: "user", ActorID: &a.UserID, Action: action, ResourceType: "storage_provider", ResourceID: strconv.FormatInt(p.ID, 10), ResourceLabel: p.Name, After: state, Result: "success"}
	if action == "storage.provider.delete" {
		event.Before, event.After, event.AfterNull = state, nil, true
	}
	return s.record(ctx, event)
}
