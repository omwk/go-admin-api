package gateway

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/omwk/go-admin-api/internal/kernel/audit"
	kernelcrypto "github.com/omwk/go-admin-api/internal/kernel/crypto"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
)

// Service performs application and secret lifecycle operations in primary transactions.
type Service struct {
	store  Store
	sealer kernelcrypto.Sealer
	now    func() time.Time
}

// NewService requires transactional persistence and a clock; issuance also needs a keyring.
func NewService(store Store, sealer kernelcrypto.Sealer, now func() time.Time) (*Service, error) {
	if store == nil || now == nil {
		return nil, errors.New("gateway lifecycle dependencies are required")
	}
	return &Service{store: store, sealer: sealer, now: now}, nil
}

// Create issues an application and its first key atomically; plaintext is returned once.
func (s *Service) Create(ctx context.Context, actor Actor, name string, owner int64, whitelist []string) (Client, IssuedSecret, error) {
	if actor.TenantID <= 0 || actor.UserID <= 0 || owner != actor.UserID || len(strings.TrimSpace(name)) == 0 || len(name) > 200 || len(whitelist) > 100 {
		return Client{}, IssuedSecret{}, Invalid()
	}
	for _, value := range whitelist {
		if _, err := netip.ParseAddr(value); err != nil {
			return Client{}, IssuedSecret{}, Invalid()
		}
	}
	client := Client{TenantID: actor.TenantID, AppID: ulid.Make().String(), Name: strings.TrimSpace(name), OwnerUserID: owner, Status: "active", IPWhitelist: append([]string{}, whitelist...), Version: 1}
	var issued IssuedSecret
	err := s.store.Transaction(ctx, func(tx context.Context) error {
		exists, err := s.store.OwnerExists(tx, actor.TenantID, owner)
		if err != nil {
			return err
		}
		if !exists {
			return ResourceUnavailable()
		}
		if err := s.store.CreateClient(tx, &client); err != nil {
			return err
		}
		issued, err = s.issue(tx, client)
		if err != nil {
			return err
		}
		return s.record(tx, actor, client.ID, "gateway.client.create", issued.ID)
	})
	if err != nil {
		return Client{}, IssuedSecret{}, err
	}
	return client, issued, nil
}

// Rotate caps live keys at two and can only shorten the old key's expiration.
func (s *Service) Rotate(ctx context.Context, actor Actor, clientID int64, overlap time.Duration) (IssuedSecret, error) {
	if overlap == 0 {
		overlap = 24 * time.Hour
	}
	if actor.TenantID <= 0 || actor.UserID <= 0 || clientID <= 0 || overlap < time.Second || overlap > 7*24*time.Hour {
		return IssuedSecret{}, Invalid()
	}
	var issued IssuedSecret
	err := s.store.Transaction(ctx, func(tx context.Context) error {
		client, err := s.store.LockClient(tx, actor.TenantID, clientID)
		if err != nil {
			return err
		}
		if client.OwnerUserID != actor.UserID {
			return ResourceUnavailable()
		}
		if client.Status != "active" {
			return authenticationFailure(80101)
		}
		now := s.now().UTC()
		if err := s.store.ExpireSecrets(tx, client.TenantID, client.ID, now); err != nil {
			return err
		}
		keys, err := s.store.Secrets(tx, client.TenantID, client.ID)
		if err != nil {
			return err
		}
		if len(keys) >= 2 {
			return errs.New(10004, 409, "应用已有两把有效密钥")
		}
		for _, key := range keys {
			expiry := now.Add(overlap)
			if key.ExpiresAt != nil && key.ExpiresAt.Before(expiry) {
				expiry = *key.ExpiresAt
			}
			if err := s.store.SetSecretExpiry(tx, client.TenantID, client.ID, key.ID, expiry); err != nil {
				return err
			}
		}
		issued, err = s.issue(tx, client)
		if err != nil {
			return err
		}
		return s.record(tx, actor, client.ID, "gateway.secret.rotate", issued.ID)
	})
	if err != nil {
		return IssuedSecret{}, err
	}
	return issued, nil
}

// Revoke makes a key unusable on the next primary-state admission.
func (s *Service) Revoke(ctx context.Context, actor Actor, clientID, secretID int64) error {
	if actor.TenantID <= 0 || actor.UserID <= 0 || clientID <= 0 || secretID <= 0 {
		return Invalid()
	}
	return s.store.Transaction(ctx, func(tx context.Context) error {
		client, err := s.store.LockClient(tx, actor.TenantID, clientID)
		if err != nil {
			return err
		}
		if client.OwnerUserID != actor.UserID {
			return ResourceUnavailable()
		}
		if err := s.store.RevokeSecret(tx, actor.TenantID, clientID, secretID); err != nil {
			return err
		}
		return s.record(tx, actor, clientID, "gateway.secret.revoke", secretID)
	})
}

// SetStatus uses optimistic locking and never depends on a metadata cache.
func (s *Service) SetStatus(ctx context.Context, actor Actor, clientID int64, version int, status string) error {
	if actor.TenantID <= 0 || actor.UserID <= 0 || clientID <= 0 || version <= 0 || status != "active" && status != "suspended" {
		return Invalid()
	}
	return s.store.Transaction(ctx, func(tx context.Context) error {
		client, err := s.store.LockClient(tx, actor.TenantID, clientID)
		if err != nil {
			return err
		}
		if client.OwnerUserID != actor.UserID {
			return ResourceUnavailable()
		}
		if err := s.store.SetClientStatus(tx, actor.TenantID, clientID, version, status); err != nil {
			return err
		}
		return s.record(tx, actor, clientID, "gateway.client.status", 0)
	})
}

func (s *Service) issue(ctx context.Context, client Client) (IssuedSecret, error) {
	if s.sealer == nil {
		return IssuedSecret{}, Unavailable()
	}
	id, err := s.store.NextSecretID(ctx)
	if err != nil {
		return IssuedSecret{}, err
	}
	random := make([]byte, 32)
	defer clear(random)
	if _, err := rand.Read(random); err != nil {
		return IssuedSecret{}, errors.New("generate gateway key")
	}
	text := base64.StdEncoding.EncodeToString(random)
	plain := []byte(text)
	defer clear(plain)
	key := Secret{ID: id, TenantID: client.TenantID, ClientID: client.ID, Fingerprint: kernelcrypto.Fingerprint(plain), SecretPrefix: text[:6], Status: "active", Version: 1}
	key.CipherText, key.Nonce, key.KeyVersion, err = s.sealer.Seal(plain, secretAAD(key))
	if err != nil {
		return IssuedSecret{}, err
	}
	if err := s.store.InsertSecret(ctx, key); err != nil {
		return IssuedSecret{}, err
	}
	return IssuedSecret{ID: id, Secret: text, SecretPrefix: key.SecretPrefix}, nil
}

func secretAAD(key Secret) string {
	return fmt.Sprintf("gateway.hmac:%d:%d:%d:%d", key.TenantID, key.ClientID, key.ID, key.Version)
}

func (s *Service) record(ctx context.Context, actor Actor, clientID int64, action string, secretID int64) error {
	return s.store.Audit(ctx, audit.Event{TenantID: actor.TenantID, ActorType: "user", ActorID: &actor.UserID, Action: action, ResourceType: "api_client", ResourceID: strconv.FormatInt(clientID, 10), After: map[string]any{"secret_id": secretID}, Result: "success"})
}
