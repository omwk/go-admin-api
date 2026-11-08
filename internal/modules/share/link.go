package share

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/crypto"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/idem"
	"golang.org/x/crypto/bcrypt"
)

// LinkService publishes frozen quotes and authorizes short-lived anonymous browsing.
type LinkService struct {
	base        *Service
	store       LinkStore
	sealer      crypto.Sealer
	cache       AccessCache
	now         func() time.Time
	verifySlots chan struct{}
}

// NewLinkService assembles existing pricing, persistence, crypto and cache ports.
func NewLinkService(d LinkDependencies) (*LinkService, error) {
	base, err := NewService(Dependencies{Store: d.Store, Idempotency: d.Idempotency, Sealer: d.Sealer, Now: d.Now})
	if err != nil {
		return nil, err
	}
	return &LinkService{base: base, store: d.Store, sealer: d.Sealer, cache: d.Cache, now: base.now, verifySlots: make(chan struct{}, 4)}, nil
}

// Issue commits a new immutable link; retries replay only its secret-free receipt.
func (s *LinkService) Issue(ctx context.Context, a Actor, oldID int64, in LinkInput, op *idem.Operation) (LinkReceipt, error) {
	in, err := normalizeLink(in, oldID, s.now().UTC())
	if err != nil {
		return LinkReceipt{}, err
	}
	if s.sealer == nil {
		return LinkReceipt{}, unavailable()
	}
	var password *string
	if in.Password != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), 12)
		if err != nil {
			return LinkReceipt{}, unavailable()
		}
		value := string(hash)
		password = &value
	}
	in.Password = ""
	for range 3 {
		out, err := s.issueAttempt(ctx, a, oldID, in, password, op)
		if !errors.Is(err, ErrTokenCollision) {
			return out, err
		}
	}
	return LinkReceipt{}, unavailable()
}

func (s *LinkService) issueAttempt(ctx context.Context, a Actor, oldID int64, in LinkInput, password *string, op *idem.Operation) (LinkReceipt, error) {
	var secret string
	var result idem.Result
	err := s.store.Consistent(ctx, func(tx context.Context) error {
		var err error
		result, err = s.base.Mutation(tx, a, op, func(tx context.Context, r idem.Result) error {
			var saved LinkReceipt
			if err := json.Unmarshal(r.Body, &saved); err != nil {
				return err
			}
			_, err := s.store.Link(tx, a.TenantID, saved.ID, false)
			return err
		}, func(tx context.Context) (idem.Result, error) {
			if oldID > 0 {
				if _, err := s.store.Link(tx, a.TenantID, oldID, true); err != nil {
					return idem.Result{}, err
				}
			}
			frozen, err := s.freeze(tx, a, in)
			if err != nil {
				return idem.Result{}, err
			}
			secret, err = generateToken(rand.Reader)
			if err != nil {
				return idem.Result{}, err
			}
			row := Link{TenantID: a.TenantID, TokenHash: tokenHash(secret), TokenPrefix: secret[:8], Type: "products", Mode: "snapshot", Title: in.Title,
				PasswordHash: password, ExpiresAt: frozen.Content.ValidUntil, MaxViews: in.MaxViews, Status: "active", ContentVersion: 1, CreatedBy: a.UserID, CreatedAt: s.now().UTC()}
			row.CustomerID, row.PriceListID = optionalID(in.CustomerID), optionalID(in.PriceListID)
			row.AllowInquiry = in.AllowInquiry == nil || *in.AllowInquiry
			frozen.Content.AllowInquiry = row.AllowInquiry
			if in.PriceListID > 0 {
				row.Type = "price_list"
			}
			frozen.Content.RequirePassword = password != nil
			if err := s.store.InsertLink(tx, &row, frozen); err != nil {
				return idem.Result{}, err
			}
			if oldID > 0 && *in.RevokeOld {
				if err := s.revoke(tx, a, oldID); err != nil {
					return idem.Result{}, err
				}
			}
			if err := s.base.audit(tx, a, "share_link", row.ID, "create"); err != nil {
				return idem.Result{}, err
			}
			return idem.JSONResult(201, LinkReceipt{ID: row.ID, Status: row.Status, SecretDelivered: true})
		})
		return err
	})
	if err != nil {
		return LinkReceipt{}, err
	}
	var out LinkReceipt
	if err := json.Unmarshal(result.Body, &out); err != nil {
		return LinkReceipt{}, err
	}
	if secret != "" {
		out.URL = "/api/v1/share/" + secret
	}
	return out, nil
}

// Revoke serializes durable revocation with every anonymous admission.
func (s *LinkService) Revoke(ctx context.Context, a Actor, id int64, op *idem.Operation) error {
	_, err := s.base.Mutation(ctx, a, op, func(tx context.Context, _ idem.Result) error {
		_, err := s.store.Link(tx, a.TenantID, id, false)
		return err
	}, func(tx context.Context) (idem.Result, error) {
		if err := s.revoke(tx, a, id); err != nil {
			return idem.Result{}, err
		}
		return idem.JSONResult(204, nil)
	})
	return err
}

func (s *LinkService) revoke(ctx context.Context, a Actor, id int64) error {
	if err := s.store.RevokeLink(ctx, a.TenantID, id, s.now().UTC()); err != nil {
		return err
	}
	return s.base.audit(ctx, a, "share_link", id, "revoke")
}

// Links returns a bounded, scope-filtered listing without recoverable secrets.
func (s *LinkService) Links(ctx context.Context, a Actor, in Query) (Page[LinkView], error) {
	if in.Status != "" && in.Status != "active" && in.Status != "revoked" && in.Status != "expired" {
		return Page[LinkView]{}, Invalid()
	}
	status := in.Status
	in.Status = ""
	in, err := NormalizeQuery(in)
	if err != nil {
		return Page[LinkView]{}, err
	}
	in.Status = status
	rows, total, err := s.store.Links(ctx, a.TenantID, in)
	out := Page[LinkView]{List: []LinkView{}, Total: total, Page: in.Page, PageSize: in.PageSize}
	for _, row := range rows {
		out.List = append(out.List, LinkView{ID: row.ID, Title: row.Title, TokenPrefix: row.TokenPrefix, Mode: row.Mode, Status: row.Status,
			MaxViews: row.MaxViews, ViewCount: row.ViewCount, ExpiresAt: row.ExpiresAt, CreatedAt: row.CreatedAt, RequirePassword: row.PasswordHash != nil})
	}
	return out, err
}

func normalizeLink(in LinkInput, oldID int64, now time.Time) (LinkInput, error) {
	in.Title = strings.TrimSpace(in.Title)
	if in.Mode == "" {
		in.Mode = "snapshot"
	}
	if in.Currency == "" {
		in.Currency = "CNY"
	}
	if in.Title == "" || !validText(in.Title, 200) || in.Mode != "snapshot" || !validCurrency(in.Currency) || in.CustomerID < 0 || in.PriceListID < 0 ||
		in.MaxViews < 0 || len(in.Items) == 0 || len(in.Items) > 100 || len(in.Password) > 72 || (in.CustomerID > 0 && in.Password == "") ||
		!utcTime(in.ExpiresAt) || (in.ExpiresAt != nil && !in.ExpiresAt.After(now)) || (oldID > 0 && in.RevokeOld == nil) || (oldID == 0 && in.RevokeOld != nil) {
		return in, Invalid()
	}
	seen := map[int64]bool{}
	for _, member := range in.Items {
		if member.SPUID <= 0 || seen[member.SPUID] || len(member.SKUIDs) > 500 {
			return in, Invalid()
		}
		seen[member.SPUID] = true
		skus := map[int64]bool{}
		for _, sku := range member.SKUIDs {
			if sku <= 0 || skus[sku] {
				return in, Invalid()
			}
			skus[sku] = true
		}
	}
	return in, nil
}

func optionalID(value int64) *int64 {
	if value == 0 {
		return nil
	}
	return &value
}

func unavailable() error { return errs.New(errs.ErrInternal, 503, "链接暂不可用") }
