package billing

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strconv"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
)

// Open creates a zero-balance wallet for an application owned by the actor.
func (s *Service) Open(ctx context.Context, a Actor, app string) (Wallet, error) {
	if _, err := ulid.ParseStrict(app); err != nil {
		return Wallet{}, Invalid()
	}
	var out Wallet
	err := s.store.Transaction(ctx, func(tx context.Context) error {
		var err error
		out, err = s.store.OpenWallet(tx, a, app, s.now().UTC())
		return err
	})
	return out, err
}

// Wallets lists only the actor's application wallets.
func (s *Service) Wallets(ctx context.Context, a Actor) ([]Wallet, error) {
	return s.store.Wallets(ctx, a)
}

// Txns lists the most recent owned-wallet ledger entries.
func (s *Service) Txns(ctx context.Context, a Actor, id int64) ([]Txn, error) {
	return s.store.Txns(ctx, a, id)
}

// Balance reads one authenticated application's settlement wallet.
func (s *Service) Balance(ctx context.Context, tenant int64, app string) (Balance, error) {
	w, err := s.store.Wallet(ctx, tenant, app)
	return w.View(), err
}

// Topup adds funds once per actor-scoped idempotency key and audits the balance change.
func (s *Service) Topup(ctx context.Context, a Actor, id, amount int64, note, key string) (Wallet, error) {
	if amount <= 0 || len(strings.TrimSpace(note)) == 0 || len(note) > 500 || len(key) < 1 || len(key) > 128 {
		return Wallet{}, Invalid()
	}
	var out Wallet
	err := s.store.Transaction(ctx, func(tx context.Context) error {
		before, err := s.store.OwnedWallet(tx, a, id)
		if err != nil {
			return err
		}
		out, err = s.store.Apply(tx, Change{Txn: Txn{TenantID: a.TenantID, WalletID: id, Type: "topup", AmountCents: amount, BalanceDeltaCents: amount,
			IdempotencyKey: "billing:topup:" + strconv.FormatInt(a.TenantID, 10) + ":" + strconv.FormatInt(a.UserID, 10) + ":" + fingerprint(key),
			RequestHash:    fingerprint(id, amount, note), ActorID: a.UserID, Note: note, OccurredAt: s.now().UTC()}, Currency: Currency, RequireActive: true})
		if err != nil {
			return err
		}
		before.BalanceCents, before.FrozenCents = out.BalanceCents-amount, out.FrozenCents
		return s.store.AuditCredit(tx, a, before, out, "billing.wallet.topup")
	})
	return out, err
}

// Mint generates random credentials; only their SHA-256 digests are persisted.
func (s *Service) Mint(ctx context.Context, a Actor, amount int64, count int, expires *time.Time) ([]CodeDelivery, error) {
	if amount <= 0 || count < 1 || count > 100 || expires != nil && !expires.After(s.now()) {
		return nil, Invalid()
	}
	out := make([]CodeDelivery, 0, count)
	err := s.store.Transaction(ctx, func(tx context.Context) error {
		for i := 0; i < count; i++ {
			raw := make([]byte, 32)
			if _, err := rand.Read(raw); err != nil {
				return Unavailable()
			}
			text := base64.RawURLEncoding.EncodeToString(raw)
			clear(raw)
			c := Code{TenantID: a.TenantID, CodeHash: codeHash(text), AmountCents: amount, Currency: Currency, Status: "unused", ExpiresAt: expires, CreatedBy: a.UserID, CreatedAt: s.now().UTC()}
			if err := s.store.InsertCode(tx, &c); err != nil {
				return err
			}
			out = append(out, CodeDelivery{ID: c.ID, Code: text})
		}
		return nil
	})
	return out, err
}

// Redeem atomically consumes a valid credential, adds funds, and appends its ledger entry.
func (s *Service) Redeem(ctx context.Context, tenant int64, app, text string) (Balance, error) {
	if len(text) != 43 {
		return Balance{}, InvalidCode()
	}
	var out Wallet
	err := s.store.Transaction(ctx, func(tx context.Context) error {
		c, err := s.store.UseCode(tx, tenant, app, codeHash(text), s.now().UTC())
		if err != nil {
			return err
		}
		w, err := s.store.Wallet(tx, tenant, app)
		if err != nil {
			return err
		}
		out, err = s.store.Apply(tx, Change{Txn: Txn{TenantID: tenant, WalletID: w.ID, Type: "topup", AmountCents: c.AmountCents, BalanceDeltaCents: c.AmountCents,
			IdempotencyKey: "billing:code:" + strconv.FormatInt(c.ID, 10), RequestHash: fingerprint(tenant, app, c.ID), OccurredAt: s.now().UTC()}, Currency: c.Currency, RequireActive: true})
		return err
	})
	return out.View(), err
}

func codeHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}
