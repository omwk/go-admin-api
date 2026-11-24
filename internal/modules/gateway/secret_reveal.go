package gateway

import (
	"context"
	"strconv"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/audit"
)

const secretRevealDailyLimit = 20

// Reveal discloses the current key only after the quota and audit transaction commits.
func (s *Service) Reveal(ctx context.Context, actor Actor, clientID, secretID int64, ip string) (RevealedSecret, error) {
	if actor.TenantID <= 0 || actor.UserID <= 0 || clientID <= 0 || secretID <= 0 {
		return RevealedSecret{}, Invalid()
	}
	var key Secret
	var plain []byte
	err := s.store.Transaction(ctx, func(tx context.Context) error {
		var err error
		key, err = s.store.LockSecretForReveal(tx, actor.TenantID, clientID, secretID)
		if err != nil {
			return err
		}
		if !secretActiveAt(key.Status, key.ExpiresAt, s.now()) {
			return ResourceUnavailable()
		}
		if s.sealer == nil {
			return Unavailable()
		}
		event := audit.Event{
			TenantID: actor.TenantID, ActorType: "user", ActorID: &actor.UserID,
			Action: "gateway.secret.reveal", ResourceType: "api_client", ResourceID: strconv.FormatInt(clientID, 10),
			After: map[string]any{"secret_id": secretID}, IP: ip, Result: "success",
		}
		if err := s.store.RecordSecretReveal(tx, event, secretRevealDailyLimit); err != nil {
			return err
		}
		plain, err = s.sealer.Open(key.CipherText, key.Nonce, secretAAD(key), key.KeyVersion)
		if err != nil {
			return Unavailable()
		}
		return nil
	})
	defer clear(plain)
	if err != nil {
		return RevealedSecret{}, err
	}
	if key.ExpiresAt != nil {
		expiry := key.ExpiresAt.UTC()
		key.ExpiresAt = &expiry
	}
	return RevealedSecret{Secret: string(plain), Prefix: key.SecretPrefix, ExpiresAt: key.ExpiresAt}, nil
}

func (s *Service) listSecrets(ctx context.Context, actor Actor, clientID int64) ([]SecretInfo, error) {
	keys, err := s.store.ListSecrets(ctx, actor, clientID)
	if err != nil {
		return nil, err
	}
	now := s.now()
	for i := range keys {
		keys[i].Revealable = secretActiveAt(keys[i].Status, keys[i].ExpiresAt, now)
	}
	return keys, nil
}

func secretActiveAt(status string, expiresAt *time.Time, now time.Time) bool {
	return status == "active" && (expiresAt == nil || now.Before(*expiresAt))
}
